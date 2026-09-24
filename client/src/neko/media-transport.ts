export interface MediaTransport {
  start(): void | Promise<void>
  stop(): void
}

export const WEB_CODECS_MODES = ['webcodecs-ws'] as const

export function isWebCodecsMode(value: string | null): boolean {
  return WEB_CODECS_MODES.some((mode) => mode === value)
}
