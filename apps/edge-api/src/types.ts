export interface Env {
  HYPERDRIVE: Hyperdrive;
  API_KEY_SESSION: DurableObjectNamespace;
  API_KEY_PEPPER: string;
  RATE_LIMIT_ENABLED: string;
  RATE_LIMIT_TOKENS_PER_SECOND: string;
  RATE_LIMIT_BURST: string;
  RATE_LIMIT_EPISODES_COST: string;
  RATE_LIMIT_BULK_DIVISOR: string;
}

export interface Principal {
  keyId: number;
  userId: number | null;
  name: string;
  prefix: string;
}

export interface Variables {
  principal: Principal;
}

export type AppBindings = { Bindings: Env; Variables: Variables };

export interface RateLimitConfig {
  enabled: boolean;
  tokensPerSecond: number;
  burst: number;
  episodesCost: number;
  bulkDivisor: number;
}

export function readRateLimitConfig(env: Env): RateLimitConfig {
  return {
    enabled: env.RATE_LIMIT_ENABLED === "true",
    tokensPerSecond: parseInt(env.RATE_LIMIT_TOKENS_PER_SECOND, 10) || 10,
    burst: parseInt(env.RATE_LIMIT_BURST, 10) || 40,
    episodesCost: parseInt(env.RATE_LIMIT_EPISODES_COST, 10) || 8,
    bulkDivisor: parseInt(env.RATE_LIMIT_BULK_DIVISOR, 10) || 25,
  };
}

export const MAX_CONCURRENT_SOCKETS_PER_KEY = 5;
export const SOCKET_MESSAGE_BURST = 20;
export const SOCKET_MESSAGE_RATE_PER_SECOND = 20;
export const WS_SEARCH_LIMIT = 10;
