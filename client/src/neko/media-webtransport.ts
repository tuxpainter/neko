import { EncodedMediaSample, parseMediaSample } from './media-protocol'
import { MediaTransport } from './media-transport'

const MAX_SAMPLE_SIZE = 16 * 1024 * 1024
const SAMPLE_HEADER_SIZE = 20
const RECORD_HEADER_SIZE = 4
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
        void this.readStream(stream).catch((error) => {
          this.fail(this.transportError('stream', error))
        })
      }
    } finally {
      streams.releaseLock()
    }
  }

  private async readStream(stream: ReadableStream<Uint8Array>) {
    const reader = stream.getReader({ mode: 'byob' })

    const readInto = async (buffer: ArrayBuffer, allowEnd: boolean): Promise<ArrayBuffer | undefined> => {
      const size = buffer.byteLength
      let offset = 0
      while (offset < size) {
        const { value, done } = await reader.read(new Uint8Array(buffer, offset, size - offset))
        if (value) {
          buffer = value.buffer as ArrayBuffer
          offset += value.byteLength
        }
        if (done) {
          if (allowEnd && offset === 0) return undefined
          if (offset !== size) throw new Error('media WebTransport stream ended during a record')
        }
      }
      return buffer
    }

    let recordHeader = new ArrayBuffer(RECORD_HEADER_SIZE)
    try {
      while (!this.stopped) {
        const header = await readInto(recordHeader, true)
        if (!header) return
        recordHeader = header
        const size = new DataView(recordHeader).getUint32(0)
        if (size < SAMPLE_HEADER_SIZE) throw new Error('media sample is smaller than its header')
        if (size > MAX_SAMPLE_SIZE) throw new Error('media sample exceeds size limit')

        const sample = await readInto(new ArrayBuffer(size), false)
        if (!sample) throw new Error('media WebTransport stream ended during a record')
        this.onSample(parseMediaSample(sample))
      }
    } finally {
      reader.releaseLock()
    }
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
