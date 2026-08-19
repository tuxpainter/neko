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
    const reader = stream.getReader()
    let chunk: Uint8Array | undefined
    let chunkOffset = 0

    const readInto = async (target: Uint8Array, allowEnd: boolean): Promise<boolean> => {
      let offset = 0
      while (offset < target.byteLength) {
        if (!chunk || chunkOffset === chunk.byteLength) {
          const result = await reader.read()
          if (result.done) {
            if (allowEnd && offset === 0) return false
            throw new Error('media WebTransport stream ended during a record')
          }
          chunk = result.value
          chunkOffset = 0
          if (chunk.byteLength === 0) continue
        }

        const length = Math.min(target.byteLength - offset, chunk.byteLength - chunkOffset)
        target.set(chunk.subarray(chunkOffset, chunkOffset + length), offset)
        offset += length
        chunkOffset += length
      }
      return true
    }

    const recordHeader = new Uint8Array(RECORD_HEADER_SIZE)
    const recordHeaderView = new DataView(recordHeader.buffer)
    try {
      while (!this.stopped && (await readInto(recordHeader, true))) {
        const size = recordHeaderView.getUint32(0)
        if (size < SAMPLE_HEADER_SIZE) throw new Error('media sample is smaller than its header')
        if (size > MAX_SAMPLE_SIZE) throw new Error('media sample exceeds size limit')

        const sample = new Uint8Array(size)
        await readInto(sample, false)
        this.onSample(parseMediaSample(sample.buffer))
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
