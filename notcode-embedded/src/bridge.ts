/**
 * stdio-мост для MCP-серверов, которые запускаются локально (npx/uvx/docker).
 *
 * Notion умеет подключать только сетевой MCP-эндпоинт, поэтому пакеты
 * вроде @playwright/mcp или @modelcontextprotocol/server-memory сами себя не
 * отдадут. Мост поднимает такой пакет дочерним процессом и проксирует в него
 * JSON-RPC из POST /bridge/:slug/mcp — тот же туннель и тот же Bearer-токен,
 * что и у самого notcode.
 *
 * Рукопожатие с дочерним процессом делается ОДИН раз и кэшируется: клиенту
 * (Notion) ответ на initialize отдаётся из кэша, поэтому со стороны Notion
 * эндпоинт остаётся stateless, а дочерний процесс не получает повторный
 * initialize (что часть реализаций считает ошибкой).
 */

import { CONFIG_FILE } from "@/config";
import { createLogger, errorMessage } from "@/utils/logger";

const log = createLogger("bridge");

/** Описание локального MCP-сервера из ~/.notcode/config.json → bridges. */
export interface BridgeSpec {
    command: string;
    args: string[];
    env: Record<string, string>;
    cwd?: string;
}

const PROTOCOL_VERSION = "2025-06-18";
const REQUEST_TIMEOUT_MS = 180_000;
const IDLE_KILL_MS = 15 * 60_000;
const BRIDGES_CACHE_MS = 1_000;

type JsonRpc = Record<string, unknown>;

interface Child {
    slug: string;
    proc: ReturnType<typeof Bun.spawn>;
    pending: Map<number, (message: JsonRpc) => void>;
    nextId: number;
    initialize: JsonRpc | null;
    lastUsed: number;
    starting: Promise<void> | null;
    signature: string;
}

const children = new Map<string, Child>();
let bridgesCache: { at: number; value: Record<string, BridgeSpec> } | null = null;
let reaper: ReturnType<typeof setInterval> | null = null;

function asStringRecord(value: unknown): Record<string, string> {
    const out: Record<string, string> = {};
    if (typeof value !== "object" || value === null) return out;
    for (const [key, item] of Object.entries(value as Record<string, unknown>)) {
        if (typeof item === "string") out[key] = item;
    }
    return out;
}

/** Список мостов живёт в том же файле конфига (ключ bridges). */
export async function loadBridges(force = false): Promise<Record<string, BridgeSpec>> {
    if (!force && bridgesCache && Date.now() - bridgesCache.at < BRIDGES_CACHE_MS) {
        return bridgesCache.value;
    }
    const out: Record<string, BridgeSpec> = {};
    try {
        const file = Bun.file(CONFIG_FILE);
        if (await file.exists()) {
            const raw = (await file.json()) as Record<string, unknown>;
            const bridges = raw.bridges;
            if (typeof bridges === "object" && bridges !== null) {
                for (const [slug, value] of Object.entries(bridges as Record<string, unknown>)) {
                    if (typeof value !== "object" || value === null) continue;
                    const spec = value as Record<string, unknown>;
                    const command = typeof spec.command === "string" ? spec.command.trim() : "";
                    if (!command) continue;
                    out[slug] = {
                        command,
                        args: Array.isArray(spec.args)
                            ? spec.args.filter((item): item is string => typeof item === "string")
                            : [],
                        env: asStringRecord(spec.env),
                        cwd: typeof spec.cwd === "string" && spec.cwd.trim() ? spec.cwd : undefined
                    };
                }
            }
        }
    } catch (error) {
        log.warn("не удалось прочитать список мостов", { error: errorMessage(error) });
    }
    bridgesCache = { at: Date.now(), value: out };
    return out;
}

function signatureOf(spec: BridgeSpec): string {
    return JSON.stringify([spec.command, spec.args, spec.env, spec.cwd ?? ""]);
}

function killChild(child: Child, reason: string): void {
    children.delete(child.slug);
    for (const resolve of child.pending.values()) {
        resolve({ jsonrpc: "2.0", error: { code: -32000, message: `мост остановлен: ${reason}` } });
    }
    child.pending.clear();
    try {
        child.proc.kill();
    } catch {
        /* процесс уже мёртв */
    }
    log.info("мост остановлен", { slug: child.slug, reason });
}

/** Простаивающие процессы не должны висеть в памяти вечно. */
function ensureReaper(): void {
    if (reaper) return;
    reaper = setInterval(() => {
        const now = Date.now();
        for (const child of [...children.values()]) {
            if (child.pending.size === 0 && now - child.lastUsed > IDLE_KILL_MS) {
                killChild(child, "простой");
            }
        }
    }, 60_000);
    reaper.unref?.();
}

function readStdout(child: Child): void {
    void (async () => {
        const decoder = new TextDecoder();
        let buffer = "";
        try {
            for await (const chunk of child.proc.stdout as ReadableStream<Uint8Array>) {
                buffer += decoder.decode(chunk, { stream: true });
                let index = buffer.indexOf("\n");
                while (index >= 0) {
                    const line = buffer.slice(0, index).trim();
                    buffer = buffer.slice(index + 1);
                    index = buffer.indexOf("\n");
                    if (!line) continue;
                    let message: JsonRpc;
                    try {
                        message = JSON.parse(line) as JsonRpc;
                    } catch {
                        // Многие пакеты пишут баннеры в stdout — это не ошибка.
                        continue;
                    }
                    const id = message.id;
                    if (typeof id === "number") {
                        const resolve = child.pending.get(id);
                        if (resolve) {
                            child.pending.delete(id);
                            resolve(message);
                        }
                    }
                }
            }
        } catch (error) {
            log.warn("поток stdout моста оборвался", { slug: child.slug, error: errorMessage(error) });
        }
        killChild(child, "процесс завершился");
    })();
}

function readStderr(child: Child): void {
    void (async () => {
        const decoder = new TextDecoder();
        try {
            for await (const chunk of child.proc.stderr as ReadableStream<Uint8Array>) {
                const text = decoder.decode(chunk, { stream: true }).trim();
                if (text) log.debug("stderr моста", { slug: child.slug, text: text.slice(0, 500) });
            }
        } catch {
            /* процесс закрылся */
        }
    })();
}

function send(child: Child, message: JsonRpc): void {
    const writer = child.proc.stdin as { write: (chunk: string) => void; flush?: () => void };
    writer.write(`${JSON.stringify(message)}\n`);
    writer.flush?.();
}

function request(child: Child, method: string, params?: unknown): Promise<JsonRpc> {
    const id = child.nextId++;
    return new Promise<JsonRpc>(resolve => {
        const timer = setTimeout(() => {
            child.pending.delete(id);
            resolve({
                jsonrpc: "2.0",
                id,
                error: { code: -32000, message: `локальный MCP-сервер не ответил за ${REQUEST_TIMEOUT_MS} мс` }
            });
        }, REQUEST_TIMEOUT_MS);
        child.pending.set(id, message => {
            clearTimeout(timer);
            resolve(message);
        });
        try {
            send(child, { jsonrpc: "2.0", id, method, ...(params === undefined ? {} : { params }) });
        } catch (error) {
            clearTimeout(timer);
            child.pending.delete(id);
            resolve({ jsonrpc: "2.0", id, error: { code: -32000, message: errorMessage(error) } });
        }
    });
}

async function spawnChild(slug: string, spec: BridgeSpec): Promise<Child> {
    const proc = Bun.spawn({
        cmd: [spec.command, ...spec.args],
        cwd: spec.cwd,
        env: { ...process.env, ...spec.env },
        stdin: "pipe",
        stdout: "pipe",
        stderr: "pipe"
    });
    const child: Child = {
        slug,
        proc,
        pending: new Map(),
        nextId: 1,
        initialize: null,
        lastUsed: Date.now(),
        starting: null,
        signature: signatureOf(spec)
    };
    children.set(slug, child);
    ensureReaper();
    readStdout(child);
    readStderr(child);
    log.info("мост запущен", { slug, command: spec.command, args: spec.args });

    // Рукопожатие от имени моста: результат отдаём клиентам из кэша.
    const response = await request(child, "initialize", {
        protocolVersion: PROTOCOL_VERSION,
        capabilities: {},
        clientInfo: { name: "neura-stdio-bridge", version: "1.0.0" }
    });
    if (response.error) {
        killChild(child, "initialize не прошёл");
        throw new Error(
            `локальный MCP-сервер не ответил на initialize: ${JSON.stringify(response.error)}`
        );
    }
    child.initialize = (response.result as JsonRpc) ?? {};
    send(child, { jsonrpc: "2.0", method: "notifications/initialized" });
    return child;
}

async function ensureChild(slug: string, spec: BridgeSpec): Promise<Child> {
    const existing = children.get(slug);
    if (existing) {
        if (existing.signature !== signatureOf(spec)) {
            // Настройки поменялись — поднимаем заново.
            killChild(existing, "настройки изменились");
        } else if (existing.starting) {
            await existing.starting;
            return existing;
        } else if (existing.initialize) {
            return existing;
        }
    }
    const started = spawnChild(slug, spec);
    // Параллельные запросы не должны плодить два процесса.
    const child = await started;
    child.starting = null;
    return child;
}

function isNotification(message: JsonRpc): boolean {
    return message.id === undefined || message.id === null;
}

/**
 * Обработка одного POST /bridge/:slug/mcp.
 * Возвращает HTTP-статус и тело в той же форме, что и /mcp у notcode.
 */
export async function handleBridgeRequest(
    slug: string,
    body: unknown
): Promise<{ status: number; body: unknown }> {
    const bridges = await loadBridges();
    const spec = bridges[slug];
    if (!spec) {
        return {
            status: 404,
            body: { error: "Bridge not found", hint: `в config.json нет bridges.${slug}` }
        };
    }

    const batch: JsonRpc[] = (Array.isArray(body) ? body : [body]).filter(
        (item): item is JsonRpc => typeof item === "object" && item !== null
    );
    if (batch.length === 0) {
        return { status: 400, body: { jsonrpc: "2.0", error: { code: -32600, message: "пустой запрос" } } };
    }

    let child: Child;
    try {
        child = await ensureChild(slug, spec);
    } catch (error) {
        return {
            status: 502,
            body: { jsonrpc: "2.0", error: { code: -32000, message: errorMessage(error) } }
        };
    }
    child.lastUsed = Date.now();

    const out: JsonRpc[] = [];
    for (const message of batch) {
        const method = typeof message.method === "string" ? message.method : "";
        if (isNotification(message)) {
            // Своё рукопожатие мы уже сделали; остальные уведомления передаём как есть.
            if (method && method !== "notifications/initialized") {
                try {
                    send(child, message);
                } catch (error) {
                    log.warn("не удалось передать уведомление", { slug, method, error: errorMessage(error) });
                }
            }
            continue;
        }

        if (method === "initialize") {
            const cached = child.initialize ?? {};
            out.push({
                jsonrpc: "2.0",
                id: message.id,
                result: { ...cached, protocolVersion: PROTOCOL_VERSION }
            });
            continue;
        }
        if (method === "ping") {
            out.push({ jsonrpc: "2.0", id: message.id, result: {} });
            continue;
        }

        const response = await request(child, method, message.params);
        // Идентификатор всегда возвращаем клиентский: внутреннюю нумерацию
        // моста клиент видеть не должен.
        out.push({ ...response, jsonrpc: "2.0", id: message.id });
    }

    if (out.length === 0) return { status: 202, body: "" };
    return { status: 200, body: out.length === 1 ? out[0] : out };
}

/** Остановка всех мостов — вызывается при завершении сервера. */
export function stopAllBridges(): void {
    for (const child of [...children.values()]) killChild(child, "выход");
    if (reaper) {
        clearInterval(reaper);
        reaper = null;
    }
}

/** Диагностика для /status. */
export function bridgeStatus(): Array<{ slug: string; running: boolean; pending: number }> {
    return [...children.values()].map(child => ({
        slug: child.slug,
        running: child.initialize !== null,
        pending: child.pending.size
    }));
}
