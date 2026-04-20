import {
  MAX_CONCURRENT_SOCKETS_PER_KEY,
  SOCKET_MESSAGE_BURST,
  SOCKET_MESSAGE_RATE_PER_SECOND,
  WS_SEARCH_LIMIT,
  readRateLimitConfig,
  type Env,
  type RateLimitConfig,
} from "../types";
import { newClient } from "../db";
import { searchTitlesQuery } from "../routes/titles";

interface BucketState {
  tokens: number;
  last: number;
}

interface SocketAttachment {
  id: number;
  keyId: number;
  prefix: string;
  bucket: BucketState;
}

interface WsInbound {
  type?: string;
  seq?: number;
  q?: string;
  types?: string[];
}

const ALLOWED_TITLE_TYPES = new Set(["movie", "tvSeries"]);

function refill(bucket: BucketState, now: number, ratePerSec: number, burst: number) {
  const elapsedSec = (now - bucket.last) / 1000;
  if (elapsedSec > 0) {
    bucket.tokens = Math.min(burst, bucket.tokens + elapsedSec * ratePerSec);
    bucket.last = now;
  }
}

export class ApiKeySession implements DurableObject {
  private state: DurableObjectState;
  private env: Env;
  private cfg: RateLimitConfig;
  private restBucket: BucketState | null = null;
  // In-memory per-message state. Not persisted; resets on eviction. Used to
  // cancel a prior in-flight search when the same socket sends a new one.
  private inflight = new Map<number, AbortController>();
  private nextSocketId = 1;

  constructor(state: DurableObjectState, env: Env) {
    this.state = state;
    this.env = env;
    this.cfg = readRateLimitConfig(env);
  }

  async fetch(request: Request): Promise<Response> {
    const url = new URL(request.url);

    if (url.pathname === "/rate/allow") {
      const cost = Math.max(1, parseFloat(url.searchParams.get("cost") ?? "1"));
      return this.rateAllow(cost);
    }

    if (url.pathname === "/ws/accept") {
      if (request.headers.get("upgrade") !== "websocket") {
        return new Response("expected websocket upgrade", { status: 400 });
      }
      return this.acceptWebSocket(url, request);
    }

    return new Response("not found", { status: 404 });
  }

  private async loadRestBucket(): Promise<BucketState> {
    if (this.restBucket) return this.restBucket;
    const stored = await this.state.storage.get<BucketState>("restBucket");
    this.restBucket = stored ?? { tokens: this.cfg.burst, last: Date.now() };
    return this.restBucket;
  }

  private async persistRestBucket() {
    if (this.restBucket) await this.state.storage.put("restBucket", this.restBucket);
  }

  private async rateAllow(cost: number): Promise<Response> {
    if (!this.cfg.enabled) return new Response(null, { status: 200 });

    const bucket = await this.loadRestBucket();
    const now = Date.now();
    refill(bucket, now, this.cfg.tokensPerSecond, this.cfg.burst);

    if (bucket.tokens >= cost) {
      bucket.tokens -= cost;
      await this.persistRestBucket();
      return new Response(null, { status: 200 });
    }

    const deficit = cost - bucket.tokens;
    const retryAfter = Math.max(1, Math.ceil(deficit / this.cfg.tokensPerSecond));
    await this.persistRestBucket();
    return new Response(null, {
      status: 429,
      headers: { "Retry-After": String(retryAfter) },
    });
  }

  private async acceptWebSocket(url: URL, request: Request): Promise<Response> {
    // Handshake rate limit: one token from REST bucket.
    const handshake = await this.rateAllow(1);
    if (handshake.status === 429) return handshake;

    // Concurrency cap.
    if (this.state.getWebSockets().length >= MAX_CONCURRENT_SOCKETS_PER_KEY) {
      return new Response(
        JSON.stringify({ error: { code: "rate_limited", message: "too many concurrent sockets" } }),
        { status: 429, headers: { "Content-Type": "application/json" } },
      );
    }

    const pair = new WebSocketPair();
    const client = pair[0];
    const server = pair[1];

    const attachment: SocketAttachment = {
      id: this.nextSocketId++,
      keyId: parseInt(url.searchParams.get("keyId") ?? "0", 10),
      prefix: url.searchParams.get("prefix") ?? "",
      bucket: { tokens: SOCKET_MESSAGE_BURST, last: Date.now() },
    };
    server.serializeAttachment(attachment);

    this.state.acceptWebSocket(server);

    return new Response(null, { status: 101, webSocket: client });
  }

  // Hibernatable WebSocket handler.
  async webSocketMessage(ws: WebSocket, data: string | ArrayBuffer): Promise<void> {
    const attachment = ws.deserializeAttachment() as SocketAttachment | null;
    if (!attachment) {
      ws.close(1011, "no attachment");
      return;
    }

    // Per-socket message rate bucket.
    const now = Date.now();
    refill(attachment.bucket, now, SOCKET_MESSAGE_RATE_PER_SECOND, SOCKET_MESSAGE_BURST);

    const raw = typeof data === "string" ? data : new TextDecoder().decode(data);

    let msg: WsInbound;
    try {
      msg = JSON.parse(raw);
    } catch {
      this.sendFrame(ws, { type: "error", seq: 0, code: "invalid_request", message: "invalid JSON" });
      ws.serializeAttachment(attachment);
      return;
    }

    if (attachment.bucket.tokens < 1) {
      this.sendFrame(ws, {
        type: "error",
        seq: msg.seq ?? 0,
        code: "rate_limited",
        message: "message rate too high",
      });
      ws.serializeAttachment(attachment);
      return;
    }
    attachment.bucket.tokens -= 1;
    ws.serializeAttachment(attachment);

    const seq = typeof msg.seq === "number" ? msg.seq : 0;

    switch (msg.type) {
      case "ping":
        this.sendFrame(ws, { type: "pong", seq });
        return;

      case "search":
        await this.handleSearch(ws, attachment, msg, seq);
        return;

      default:
        this.sendFrame(ws, {
          type: "error",
          seq,
          code: "invalid_request",
          message: "unknown message type",
        });
        return;
    }
  }

  private async handleSearch(
    ws: WebSocket,
    attachment: SocketAttachment,
    msg: WsInbound,
    seq: number,
  ) {
    const q = (msg.q ?? "").trim();
    if (q.length < 2) {
      this.sendFrame(ws, {
        type: "error",
        seq,
        code: "invalid_request",
        message: "q must be at least 2 characters",
      });
      return;
    }

    let types = ["movie", "tvSeries"];
    if (Array.isArray(msg.types) && msg.types.length > 0) {
      const filtered: string[] = [];
      for (const t of msg.types) {
        if (!ALLOWED_TITLE_TYPES.has(t)) {
          this.sendFrame(ws, {
            type: "error",
            seq,
            code: "invalid_request",
            message: "types must be movie and/or tvSeries",
          });
          return;
        }
        filtered.push(t);
      }
      if (filtered.length > 0) types = filtered;
    }

    // Cancel any prior in-flight searches for this socket.
    for (const [prevSeq, controller] of this.inflight) {
      controller.abort();
      this.sendFrame(ws, { type: "cancelled", seq: prevSeq });
    }
    this.inflight.clear();

    const controller = new AbortController();
    this.inflight.set(seq, controller);

    const sql = newClient(this.env);
    try {
      const response = await searchTitlesQuery(sql, q, types, WS_SEARCH_LIMIT);
      if (controller.signal.aborted) return;
      this.sendFrame(ws, {
        type: "result",
        seq,
        results: response.results,
        meta: response.meta,
      });
    } catch (e) {
      if (controller.signal.aborted) {
        this.sendFrame(ws, { type: "cancelled", seq });
        return;
      }
      console.error("ws search failed", e);
      this.sendFrame(ws, {
        type: "error",
        seq,
        code: "internal_error",
        message: "internal server error",
      });
    } finally {
      this.inflight.delete(seq);
      this.state.waitUntil(sql.end({ timeout: 5 }));
    }
  }

  private sendFrame(ws: WebSocket, payload: Record<string, unknown>) {
    try {
      ws.send(JSON.stringify(payload));
    } catch (e) {
      console.warn("ws send failed", e);
    }
  }

  async webSocketClose(ws: WebSocket, code: number, reason: string, wasClean: boolean): Promise<void> {
    // Abort any in-flight work. Map keyed by seq; we don't tag controllers to
    // specific sockets, so closing any socket aborts all pending work on this
    // DO. Acceptable: the DO is per-API-key, and sockets share the limiter.
    for (const controller of this.inflight.values()) controller.abort();
    this.inflight.clear();
    try {
      ws.close(code, reason);
    } catch {
      // already closed
    }
  }

  async webSocketError(ws: WebSocket, error: unknown): Promise<void> {
    console.warn("ws error", error);
    try {
      ws.close(1011, "error");
    } catch {
      // already closed
    }
  }
}
