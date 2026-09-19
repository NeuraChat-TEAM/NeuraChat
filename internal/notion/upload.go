package notion

// Реальная загрузка файлов в чат ассистента — повторяет цепочку
// веб-клиента из HAR:
//  1. POST /api/v3/getUploadFileUrlForAssistantChatTranscriptUpload — подписанная S3-форма;
//  2. POST на signedUploadPostUrl (multipart/form-data, поля + file) — сам файл;
//  3. POST /api/v3/processAgentAttachment — модерация/разбор файла;
//  4. POST /api/v3/syncRecordValuesMain — ждём task_output и берём metadata шага.
//
// Результат — готовый Attachment, который buildBody кладёт в transcript
// отдельным шагом рядом с сообщением пользователя.

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"neura/internal/uid"
)

const (
	uploadURLPath     = "/api/v3/getUploadFileUrlForAssistantChatTranscriptUpload"
	processAttachPath = "/api/v3/processAgentAttachment"
	syncRecordsPath   = "/api/v3/syncRecordValuesMain"

	// Политика S3 в ответе Notion разрешает до 20 MiB на файл.
	maxUploadBytes = 20 * 1024 * 1024
)

// UploadResult — то, что уезжает во фронтенд и возвращается в следующем
// сообщении в поле attachments.
type UploadResult struct {
	Attachment
	SizeBytes  int    `json:"sizeBytes"`
	PreviewURL string `json:"previewUrl,omitempty"`
}

// GuessContentType определяет MIME по имени, если браузер его не прислал.
func GuessContentType(name, given string) string {
	if strings.TrimSpace(given) != "" {
		return given
	}
	if byExt := mime.TypeByExtension(strings.ToLower(filepath.Ext(name))); byExt != "" {
		return byExt
	}
	return "application/octet-stream"
}

// Notion проверяет расширение вложения и на незнакомое (.har, .ndjson,
// .map, .cast…) отвечает 400 ValidationError «File type not allowed».
var allowedUploadExt = map[string]bool{
	".txt": true, ".md": true, ".markdown": true, ".json": true, ".csv": true,
	".tsv": true, ".yaml": true, ".yml": true, ".xml": true, ".html": true,
	".htm": true, ".css": true, ".js": true, ".jsx": true, ".ts": true,
	".tsx": true, ".go": true, ".rs": true, ".py": true, ".rb": true,
	".php": true, ".java": true, ".kt": true, ".swift": true, ".c": true,
	".h": true, ".cpp": true, ".cs": true, ".sh": true, ".sql": true,
	".pdf": true, ".png": true, ".jpg": true, ".jpeg": true, ".gif": true,
	".webp": true, ".svg": true, ".bmp": true, ".heic": true, ".zip": true,
	".doc": true, ".docx": true, ".xls": true, ".xlsx": true, ".ppt": true,
	".pptx": true, ".mp3": true, ".wav": true, ".mp4": true, ".mov": true,
}

// sanitizeUpload гарантирует разрешённое расширение для ЛЮБОГО файла:
//   - текстовый JSON (.har, .ndjson, .map…)  -> имя + ".json";
//   - любой другой UTF-8 текст (.log, .cast, .env…) -> имя + ".txt";
//   - бинарник (.exe, .dll, .bin, .7z…)         -> реальный zip с одним
//     файлом внутри, т.е. имя + ".zip".
//
// Содержимое текстовых файлов уезжает байт в байт, у бинарных меняется
// только контейнер. Так Notion больше не отвечает 400 «File type not allowed».
func sanitizeUpload(fileName, contentType string, data []byte) (string, string, []byte) {
	if allowedUploadExt[strings.ToLower(filepath.Ext(fileName))] {
		return fileName, contentType, data
	}
	if utf8.Valid(data) {
		if json.Valid(bytes.TrimSpace(data)) {
			return fileName + ".json", "application/json", data
		}
		return fileName + ".txt", "text/plain; charset=utf-8", data
	}
	if zipped, err := zipSingleFile(fileName, data); err == nil {
		return fileName + ".zip", "application/zip", zipped
	}
	return fileName, contentType, data
}

// zipSingleFile кладёт один файл в zip-контейнер с исходным именем внутри.
func zipSingleFile(name string, data []byte) ([]byte, error) {
	buf := &bytes.Buffer{}
	writer := zip.NewWriter(buf)
	entry, err := writer.Create(filepath.Base(name))
	if err != nil {
		return nil, err
	}
	if _, err := entry.Write(data); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// UploadAttachment загружает один файл в thread указанного локального чата.
func (r *Runtime) UploadAttachment(
	ctx context.Context,
	conversationID, fileName, contentType string,
	data []byte,
) (UploadResult, error) {
	var out UploadResult

	cfg, err := r.client.require()
	if err != nil {
		return out, err
	}
	fileName = strings.TrimSpace(fileName)
	if fileName == "" {
		fileName = "file"
	}
	if len(data) == 0 {
		return out, errors.New("пустой файл")
	}
	if len(data) > maxUploadBytes {
		return out, fmt.Errorf("файл больше 20 MB (%d байт)", len(data))
	}
	contentType = GuessContentType(fileName, contentType)
	// Иначе Notion вернёт 400 «File type not allowed» (например, на .har).
	fileName, contentType, data = sanitizeUpload(fileName, contentType, data)
	if len(data) > maxUploadBytes {
		return out, fmt.Errorf("файл больше 20 MB после упаковки (%d байт)", len(data))
	}

	if conversationID == "" {
		conversationID = uid.New()
	}
	r.mu.Lock()
	convo := r.state(conversationID, cfg.SpaceID)
	pointer := r.threadPointer(convo)
	createThread := !convo.Started
	r.mu.Unlock()

	// На S3 файл уезжает с техническим именем, как и в веб-клиенте:
	// так не ломаются кириллица и пробелы в ключе бакета.
	uploadName := uid.New() + strings.ToLower(filepath.Ext(fileName))

	ticket, err := r.client.PostJSON(ctx, uploadURLPath, map[string]interface{}{
		"name":                                  uploadName,
		"contentType":                           contentType,
		"assistantChatTranscriptSessionPointer": pointer,
		"contentLength":                         len(data),
		"createThread":                          createThread,
		"agentMemorySettings": map[string]interface{}{
			"useMemories":             true,
			"excludeChatFromMemories": false,
		},
	})
	if err != nil {
		return out, err
	}

	fileURL, _ := ticket["url"].(string)
	signedGet, _ := ticket["signedGetUrl"].(string)
	postURL, _ := ticket["signedUploadPostUrl"].(string)
	fields, _ := ticket["fields"].(map[string]interface{})
	if fileURL == "" || postURL == "" {
		return out, errors.New("Notion не выдал ссылку для загрузки")
	}

	if err := r.client.uploadToS3(ctx, postURL, fields, uploadName, contentType, data); err != nil {
		return out, err
	}

	// Шаг обработки: без него модель не видит содержимое файла.
	processed, err := r.client.PostJSON(ctx, processAttachPath, map[string]interface{}{
		"url":              fileURL,
		"spaceId":          cfg.SpaceID,
		"aiSessionPointer": pointer,
		"permissionRecord": pointer,
		"source":           "user_upload",
		"clientVersion":    cfg.Headers["notion-client-version"],
	})
	if err != nil {
		return out, err
	}
	outputKey, _ := processed["outputKey"].(string)

	metadata := map[string]interface{}{"fileSizeBytes": len(data)}
	if outputKey != "" {
		if result, err := r.client.waitTaskOutput(ctx, cfg.SpaceID, outputKey); err == nil {
			for key, value := range result {
				metadata[key] = value
			}
		}
	}

	out = UploadResult{
		Attachment: Attachment{
			StepID:      uid.New(),
			StepType:    "attachment",
			FileURL:     fileURL,
			SignedGet:   signedGet,
			FileName:    fileName,
			ContentType: contentType,
			Metadata:    metadata,
		},
		SizeBytes: len(data),
	}
	if strings.HasPrefix(contentType, "image/") {
		out.PreviewURL = signedGet
	}
	return out, nil
}

// uploadToS3 отправляет multipart-форму без Notion-заголовков и без cookie:
// подпись уже в полях, лишние заголовки S3 отклонит.
func (c *Client) uploadToS3(
	ctx context.Context,
	postURL string,
	fields map[string]interface{},
	uploadName, contentType string,
	data []byte,
) error {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	// Порядок важен: все поля политики идут до file.
	for name, value := range fields {
		if text, ok := value.(string); ok {
			if err := writer.WriteField(name, text); err != nil {
				return err
			}
		}
	}
	part, err := writer.CreateFormFile("file", uploadName)
	if err != nil {
		return err
	}
	if _, err := part.Write(data); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}

	entry := c.beginDebug(&Config{Origin: postURL}, "(s3 upload)", []byte(
		fmt.Sprintf("multipart %s, %d байт, %s", uploadName, len(data), contentType),
	))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, postURL, body)
	if err != nil {
		c.finishDebug(entry, err)
		return err
	}
	req.Header.Set("content-type", writer.FormDataContentType())

	resp, err := c.http.Do(req)
	if err != nil {
		c.finishDebug(entry, err)
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	entry.Status = resp.StatusCode
	c.appendDebug(entry, raw)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := fmt.Errorf("хранилище вернуло %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
		c.finishDebug(entry, err)
		return err
	}
	c.finishDebug(entry, nil)
	return nil
}

// waitTaskOutput опрашивает task_output, пока разбор файла не завершится.
// Картинки обычно готовы за 1–2 секунды, PDF может думать дольше.
func (c *Client) waitTaskOutput(ctx context.Context, spaceID, outputKey string) (map[string]interface{}, error) {
	body := map[string]interface{}{
		"requests": []interface{}{map[string]interface{}{
			"pointer": map[string]interface{}{
				"table": "task_output", "id": outputKey, "spaceId": spaceID,
			},
			"version": -1,
		}},
		"spacePointer": map[string]interface{}{"table": "space", "id": spaceID},
	}

	delay := 400 * time.Millisecond
	for attempt := 0; attempt < 12; attempt++ {
		response, err := c.PostJSON(ctx, syncRecordsPath, body)
		if err != nil {
			return nil, err
		}
		value := digMap(response, "recordMap", "task_output", outputKey, "value", "value")
		status, _ := value["status"].(string)
		if status == "complete" {
			if data := digMap(value, "value", "result", "data"); len(data) > 0 {
				return data, nil
			}
			return map[string]interface{}{}, nil
		}
		if status == "failure" || status == "error" {
			return nil, errors.New("Notion не смог обработать файл")
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
		}
		if delay < 2*time.Second {
			delay += 200 * time.Millisecond
		}
	}
	return nil, errors.New("обработка файла затянулась")
}

const signedURLsPath = "/api/v3/getSignedFileUrls"

// FileContent — содержимое файла из шага computer-file.
type FileContent struct {
	FileName    string `json:"fileName"`
	ContentType string `json:"contentType"`
	SizeBytes   int    `json:"sizeBytes"`
	// Текст отдаём как есть, бинарь — base64 (для картинок и скачивания).
	Text       string `json:"text,omitempty"`
	DataBase64 string `json:"dataBase64,omitempty"`
	SignedURL  string `json:"signedUrl,omitempty"`
}

// FetchAttachment достаёт содержимое файла, который ассистент собрал на
// «компьютере»: сначала подписанная ссылка, затем обычный GET.
//
// fileURL приходит в виде attachment:<uuid>:<name> либо уже готовой https-ссылкой.
func (r *Runtime) FetchAttachment(ctx context.Context, fileURL, fileName string) (FileContent, error) {
	var out FileContent
	cfg, err := r.client.require()
	if err != nil {
		return out, err
	}
	fileURL = strings.TrimSpace(fileURL)
	if fileURL == "" {
		return out, errors.New("пустая ссылка на файл")
	}
	if fileName == "" {
		parts := strings.Split(fileURL, ":")
		fileName = parts[len(parts)-1]
	}

	// Notion выдаёт подписанную ссылку почти на любой запрос, но валидной
	// оказывается только та, что подписана под «правильным» thread'ом —
	// остальные отдают 403 уже на самом скачивании. Поэтому собираем все
	// варианты и качаем первый, который реально открылся.
	links, err := r.resolveFileURLs(ctx, cfg, fileURL, fileName)
	if err != nil {
		return out, err
	}

	var (
		data        []byte
		contentType string
		direct      string
		lastErr     error
	)
	for _, link := range links {
		body, ctype, getErr := r.client.getBytes(ctx, link)
		if getErr != nil {
			lastErr = getErr
			continue
		}
		data, contentType, direct, lastErr = body, ctype, link, nil
		break
	}
	if lastErr != nil {
		return out, lastErr
	}
	if direct == "" {
		return out, fmt.Errorf("Notion не выдал ссылку на файл %s", fileName)
	}

	out = FileContent{
		FileName:    fileName,
		ContentType: GuessContentType(fileName, contentType),
		SizeBytes:   len(data),
		SignedURL:   direct,
	}
	if utf8.Valid(data) && !strings.HasPrefix(out.ContentType, "image/") &&
		!strings.HasPrefix(out.ContentType, "application/zip") {
		out.Text = string(data)
	} else {
		out.DataBase64 = base64.StdEncoding.EncodeToString(data)
	}
	return out, nil
}

// Файлы ассистента (artifact / computer-file) подписываются ТОЛЬКО под
// permissionRecord своего thread'а — с пойнтером space Notion отвечает
// пустым списком, и раньше это выглядело как «Notion не выдал ссылку на файл».
const fileContentURLPath = "/api/v3/getFileContentURLForAgentThread"

// attachmentFileID достаёт uuid из ссылки attachment:<fileId>:<name>.
func attachmentFileID(ref string) string {
	if !strings.HasPrefix(ref, "attachment:") {
		return ""
	}
	parts := strings.Split(ref, ":")
	if len(parts) < 3 {
		return ""
	}
	return strings.TrimSpace(parts[1])
}

// threadPointers — все известные thread'ы (свежие первыми). Файл мог быть
// создан в любом из открытых чатов, поэтому перебираем их по очереди.
func (r *Runtime) threadPointers(fallbackSpace string) []map[string]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	type row struct {
		id, space string
		at        time.Time
	}
	rows := make([]row, 0, len(r.convos))
	for _, convo := range r.convos {
		if strings.TrimSpace(convo.ThreadID) == "" {
			continue
		}
		space := convo.SpaceID
		if strings.TrimSpace(space) == "" {
			space = fallbackSpace
		}
		rows = append(rows, row{id: convo.ThreadID, space: space, at: convo.LastUpdated})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].at.After(rows[j].at) })
	out := make([]map[string]string, 0, len(rows))
	for _, item := range rows {
		out = append(out, map[string]string{"id": item.id, "spaceId": item.space})
	}
	return out
}

// resolveFileURL повторяет цепочку веб-клиента из HAR:
//  1. getFileContentURLForAgentThread (spaceId + threadId + fileId) — основной путь;
//  2. getSignedFileUrls с permissionRecord на thread;
//  3. как последний шанс — permissionRecord на space.
func (r *Runtime) resolveFileURLs(ctx context.Context, cfg *Config, fileURL, fileName string) ([]string, error) {
	if strings.HasPrefix(fileURL, "http") {
		return []string{fileURL}, nil
	}
	pointers := r.threadPointers(cfg.SpaceID)

	var links []string
	add := func(link string) {
		if !strings.HasPrefix(link, "http") {
			return
		}
		for _, have := range links {
			if have == link {
				return
			}
		}
		links = append(links, link)
	}

	if fileID := attachmentFileID(fileURL); fileID != "" {
		for _, pointer := range pointers {
			got, err := r.client.PostJSON(ctx, fileContentURLPath, map[string]interface{}{
				"spaceId":             pointer["spaceId"],
				"threadId":            pointer["id"],
				"fileId":              fileID,
				"includeFileMetadata": true,
			})
			if err != nil {
				continue
			}
			for _, field := range []string{"url", "signedUrl", "fileUrl"} {
				link, _ := got[field].(string)
				add(link)
			}
		}
	}

	requests := make([]interface{}, 0, len(pointers)+1)
	for _, pointer := range pointers {
		requests = append(requests, map[string]interface{}{
			"url":          fileURL,
			"download":     false,
			"downloadName": fileName,
			"permissionRecord": map[string]interface{}{
				"table": "thread", "id": pointer["id"], "spaceId": pointer["spaceId"],
			},
		})
	}
	requests = append(requests, map[string]interface{}{
		"url":              fileURL,
		"download":         false,
		"downloadName":     fileName,
		"permissionRecord": map[string]interface{}{"table": "space", "id": cfg.SpaceID},
	})
	for _, request := range requests {
		signed, err := r.client.PostJSON(ctx, signedURLsPath, map[string]interface{}{
			"urls": []interface{}{request},
		})
		if err != nil {
			continue
		}
		add(firstSignedURL(signed))
	}
	if len(links) == 0 {
		return nil, fmt.Errorf("Notion не выдал ссылку на файл %s", fileName)
	}
	return links, nil
}

// firstSignedURL достаёт первую http-ссылку из ответа getSignedFileUrls.
// Notion отдаёт то массив строк, то массив объектов {signedUrl|url},
// а иногда кладёт его в ключ urls — разбираем все варианты.
func firstSignedURL(payload map[string]interface{}) string {
	for _, key := range []string{"signedUrls", "urls", "signedGetUrls"} {
		list, ok := payload[key].([]interface{})
		if !ok {
			continue
		}
		for _, item := range list {
			switch value := item.(type) {
			case string:
				if strings.HasPrefix(value, "http") {
					return value
				}
			case map[string]interface{}:
				for _, field := range []string{"signedUrl", "url", "signedGetUrl"} {
					if text, _ := value[field].(string); strings.HasPrefix(text, "http") {
						return text
					}
				}
			}
		}
	}
	return ""
}

// getBytes скачивает файл. Подписанная ссылка Notion иногда требует
// cookie-сессию (file.notion.so), а иногда, наоборот, ломается от лишних
// заголовков (presigned S3 отвечает 403). Поэтому пробуем оба варианта.
func (c *Client) getBytes(ctx context.Context, url string) ([]byte, string, error) {
	withHeaders := strings.Contains(url, "notion.so") || strings.Contains(url, "notion.com") ||
		strings.Contains(url, "notion.site")
	data, ctype, err := c.getBytesOnce(ctx, url, withHeaders)
	if err == nil {
		return data, ctype, nil
	}
	return c.getBytesOnce(ctx, url, !withHeaders)
}

func (c *Client) getBytesOnce(ctx context.Context, url string, sendHeaders bool) ([]byte, string, error) {
	cfg, err := c.require()
	if err != nil {
		return nil, "", err
	}
	entry := c.beginDebug(&Config{Origin: url}, "(get file)", nil)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		c.finishDebug(entry, err)
		return nil, "", err
	}
	if sendHeaders {
		for name, value := range cfg.Headers {
			if blockedHeaders[strings.ToLower(name)] {
				continue
			}
			req.Header.Set(name, value)
		}
	}
	resp, err := c.http.Do(req)
	if err != nil {
		c.finishDebug(entry, err)
		return nil, "", err
	}
	defer resp.Body.Close()
	entry.Status = resp.StatusCode
	data, readErr := io.ReadAll(io.LimitReader(resp.Body, maxUploadBytes))
	if readErr != nil {
		c.finishDebug(entry, readErr)
		return nil, "", readErr
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := fmt.Errorf("файл недоступен (%d)", resp.StatusCode)
		c.finishDebug(entry, err)
		return nil, "", err
	}
	c.finishDebug(entry, nil)
	return data, resp.Header.Get("content-type"), nil
}

// digMap безопасно идёт вглубь по вложенным объектам JSON.
func digMap(source map[string]interface{}, path ...string) map[string]interface{} {
	current := source
	for _, key := range path {
		next, ok := current[key].(map[string]interface{})
		if !ok {
			return nil
		}
		current = next
	}
	return current
}
