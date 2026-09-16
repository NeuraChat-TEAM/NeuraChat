package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite" // pure-Go driver, no cgo toolchain required

	"neura/internal/uid"
)

// Thread is a chat in the sidebar. SpaceID привязывает чат к воркспейсу:
// каждый воркспейс показывает только свою историю.
type Thread struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	SpaceID   string `json:"spaceId"`
	CreatedAt int64  `json:"createdAt"`
	UpdatedAt int64  `json:"updatedAt"`
}

// Message is one persisted chat turn. Parts holds the rendered timeline
// (text / reasoning / tool cards) as JSON so reloads look identical.
type Message struct {
	ID        string `json:"id"`
	ThreadID  string `json:"threadId"`
	Role      string `json:"role"`
	Content   string `json:"content"`
	Parts     string `json:"parts"`
	CreatedAt int64  `json:"createdAt"`
}

type Store struct{ db *sql.DB }

func Open(dir string) (*Store, error) {
	// WAL + NORMAL sync keeps streaming writes cheap without risking the file.
	dsn := "file:" + filepath.Join(dir, "neura.db") +
		"?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // SQLite writes are serialized anyway

	schema := `
CREATE TABLE IF NOT EXISTS threads (
  id TEXT PRIMARY KEY,
  title TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS messages (
  id TEXT PRIMARY KEY,
  thread_id TEXT NOT NULL REFERENCES threads(id) ON DELETE CASCADE,
  role TEXT NOT NULL,
  content TEXT NOT NULL DEFAULT '',
  parts TEXT NOT NULL DEFAULT '[]',
  created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS messages_thread_idx ON messages(thread_id, created_at);
CREATE TABLE IF NOT EXISTS kv (k TEXT PRIMARY KEY, v TEXT NOT NULL);
`
	if _, err := db.Exec(schema); err != nil {
		return nil, err
	}
	// Миграция для баз, созданных до разделения чатов по воркспейсам.
	// Ошибку игнорируем: колонка уже может существовать.
	_, _ = db.Exec(`ALTER TABLE threads ADD COLUMN space_id TEXT NOT NULL DEFAULT ''`)
	_, _ = db.Exec(`CREATE INDEX IF NOT EXISTS threads_space_idx ON threads(space_id, updated_at)`)
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func now() int64 { return time.Now().UnixMilli() }

func (s *Store) CreateThread(title, spaceID string) (Thread, error) {
	thread := Thread{ID: uid.New(), Title: title, SpaceID: spaceID, CreatedAt: now(), UpdatedAt: now()}
	_, err := s.db.Exec(`INSERT INTO threads (id,title,space_id,created_at,updated_at) VALUES (?,?,?,?,?)`,
		thread.ID, thread.Title, thread.SpaceID, thread.CreatedAt, thread.UpdatedAt)
	return thread, err
}

// ListThreads отдаёт чаты активного воркспейса. Чаты без привязки (созданные
// до миграции) показываем везде, чтобы старая история не пропала.
func (s *Store) ListThreads(spaceID string) ([]Thread, error) {
	rows, err := s.db.Query(`
SELECT id,title,space_id,created_at,updated_at FROM threads
WHERE ?1 = '' OR space_id = ?1 OR space_id = ''
ORDER BY updated_at DESC`, spaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Thread{}
	for rows.Next() {
		var thread Thread
		if err := rows.Scan(&thread.ID, &thread.Title, &thread.SpaceID, &thread.CreatedAt, &thread.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, thread)
	}
	return out, rows.Err()
}

// AdoptThread закрепляет чат за воркспейсом, если он ещё ничей.
func (s *Store) AdoptThread(id, spaceID string) error {
	if id == "" || spaceID == "" {
		return nil
	}
	_, err := s.db.Exec(`UPDATE threads SET space_id=? WHERE id=? AND space_id=''`, spaceID, id)
	return err
}

// SearchThreads ищет по названиям и по тексту сообщений внутри воркспейса.
func (s *Store) SearchThreads(spaceID, query string, limit int) ([]Thread, error) {
	if limit <= 0 {
		limit = 30
	}
	like := "%" + query + "%"
	rows, err := s.db.Query(`
SELECT DISTINCT t.id, t.title, t.space_id, t.created_at, t.updated_at
FROM threads t LEFT JOIN messages m ON m.thread_id = t.id
WHERE (?1 = '' OR t.space_id = ?1 OR t.space_id = '')
  AND (t.title LIKE ?2 OR m.content LIKE ?2)
ORDER BY t.updated_at DESC LIMIT ?3`, spaceID, like, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Thread{}
	for rows.Next() {
		var thread Thread
		if err := rows.Scan(&thread.ID, &thread.Title, &thread.SpaceID, &thread.CreatedAt, &thread.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, thread)
	}
	return out, rows.Err()
}

func (s *Store) RenameThread(id, title string) error {
	if id == "" {
		return errors.New("не указан чат")
	}
	_, err := s.db.Exec(`UPDATE threads SET title=?, updated_at=? WHERE id=?`, title, now(), id)
	return err
}

func (s *Store) TouchThread(id string) error {
	_, err := s.db.Exec(`UPDATE threads SET updated_at=? WHERE id=?`, now(), id)
	return err
}

func (s *Store) DeleteThread(id string) error {
	_, err := s.db.Exec(`DELETE FROM threads WHERE id=?`, id)
	return err
}

func (s *Store) SaveMessage(message Message) error {
	if message.ID == "" {
		message.ID = uid.New()
	}
	if message.CreatedAt == 0 {
		message.CreatedAt = now()
	}
	if message.Parts == "" {
		message.Parts = "[]"
	}
	if !json.Valid([]byte(message.Parts)) {
		message.Parts = "[]"
	}
	if _, err := s.db.Exec(`
INSERT INTO messages (id,thread_id,role,content,parts,created_at) VALUES (?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET content=excluded.content, parts=excluded.parts`,
		message.ID, message.ThreadID, message.Role, message.Content, message.Parts, message.CreatedAt); err != nil {
		return err
	}
	return s.TouchThread(message.ThreadID)
}

func (s *Store) LoadMessages(threadID string) ([]Message, error) {
	rows, err := s.db.Query(`SELECT id,thread_id,role,content,parts,created_at FROM messages WHERE thread_id=? ORDER BY created_at`, threadID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Message{}
	for rows.Next() {
		var message Message
		if err := rows.Scan(&message.ID, &message.ThreadID, &message.Role, &message.Content, &message.Parts, &message.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, message)
	}
	return out, rows.Err()
}

// DeleteMessagesFrom drops a message and everything after it (used on edit).
func (s *Store) DeleteMessagesFrom(threadID, messageID string) error {
	_, err := s.db.Exec(`
DELETE FROM messages WHERE thread_id=? AND created_at >= (
  SELECT created_at FROM messages WHERE id=?
)`, threadID, messageID)
	return err
}

func (s *Store) SetKV(key, value string) error {
	_, err := s.db.Exec(`INSERT INTO kv (k,v) VALUES (?,?) ON CONFLICT(k) DO UPDATE SET v=excluded.v`, key, value)
	return err
}

func (s *Store) GetKV(key string) (string, error) {
	var value string
	err := s.db.QueryRow(`SELECT v FROM kv WHERE k=?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return value, err
}
