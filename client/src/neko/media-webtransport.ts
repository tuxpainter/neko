import { EncodedMediaSample, parseMediaSample } from './media-protocol'
import { MediaTransport } from './media-transport'

const MAX_SAMPLE_SIZE = 16 * 1024 * 1024
const SERVER_CERTIFICATE_HASH = process.env.VUE_APP_SERVER_CERTIFICATE_HASH

export class MediaWebTransport implements MediaTransport {
  private transport?: WebTransport
  private stopped = false

  constructor(
    private readonly url: string,
    private readonly onSample: (sample: EncodedMediaSample) => void,
    private readonly onError: (error: Error) => void,
  ) {}

  async start() {
    if (this.transport || this.stopped) return

    const options = SERVER_CERTIFICATE_HASH
      ? {
          serverCertificateHashes: [
            {
              algorithm: 'sha-256' as const,
              value: Uint8Array.from(atob(SERVER_CERTIFICATE_HASH), (character) => character.charCodeAt(0)),
            },
          ],
        }
      : undefined
    const transport = new WebTransport(this.url, options)
    this.transport = transport
    transport.closed.then(
      () => {
        if (!this.stopped) this.fail(new Error('media WebTransport closed'))
      },
      (error) => this.fail(this.transportError('session', error)),
    )

    try {
      await transport.ready
      if (this.stopped) return
      void this.readStreams(transport).catch((error) => {
        this.fail(this.transportError('stream', error))
      })
    } catch (error) {
      this.fail(this.transportError('handshake', error))
    }
  }

  stop() {
    if (this.stopped) return
    this.stopped = true
    this.transport?.close()
    this.transport = undefined
  }

  private async readStreams(transport: WebTransport) {
    const streams = transport.incomingUnidirectionalStreams.getReader()
    try {
      while (!this.stopped) {
        const { value: stream, done } = await streams.read()
        if (done) return
        this.onSample(parseMediaSample(await this.readSample(stream)))
      }
    } finally {
      streams.releaseLock()
    }
  }

  private async readSample(stream: ReadableStream<Uint8Array>): Promise<ArrayBuffer> {
    const reader = stream.getReader()
    const chunks: Uint8Array[] = []
    let size = 0
    try {
      for (;;) {
        const { value, done } = await reader.read()
        if (done) break
        size += value.byteLength
        if (size > MAX_SAMPLE_SIZE) throw new Error('media sample exceeds size limit')
        chunks.push(value)
      }
    } finally {
      reader.releaseLock()
    }

    const sample = new Uint8Array(size)
    let offset = 0
    for (const chunk of chunks) {
      sample.set(chunk, offset)
      offset += chunk.byteLength
    }
    return sample.buffer
  }

  private fail(error: Error) {
    if (this.stopped) return
    this.stop()
    this.onError(error)
  }

  private transportError(stage: string, error: unknown): Error {
    const cause = error as { message?: string; source?: string; streamErrorCode?: number | null }
    const endpoint = new URL(this.url)
    const details = [
      cause.source && `source=${cause.source}`,
      cause.streamErrorCode != null && `code=${cause.streamErrorCode}`,
    ]
      .filter(Boolean)
      .join(', ')
    return new Error(
      `media WebTransport ${stage} failed for ${endpoint.origin}${endpoint.pathname}: ${
        cause.message || String(error)
      }${details ? ` (${details})` : ''}`,
    )
  }
}
