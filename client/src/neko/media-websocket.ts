import { EncodedMediaSample, parseMediaSample } from './media-protocol'

export class MediaWebSocket {
  private socket?: WebSocket
  private stopped = false

  constructor(
    private readonly url: string,
    private readonly onSample: (sample: EncodedMediaSample) => void,
    private readonly onError: (error: Error) => void,
  ) {}

  start() {
    if (this.socket || this.stopped) return

    const socket = new WebSocket(this.url)
    socket.binaryType = 'arraybuffer'
    socket.onmessage = ({ data }) => {
      try {
        if (!(data instanceof ArrayBuffer)) throw new Error('media websocket sent a non-binary message')
        this.onSample(parseMediaSample(data))
      } catch (error) {
        this.fail(error instanceof Error ? error : new Error(String(error)))
      }
    }
    socket.onerror = () => this.fail(new Error('media websocket failed'))
    socket.onclose = () => {
      if (!this.stopped) this.fail(new Error('media websocket closed'))
    }
    this.socket = socket
  }

  stop() {
    if (this.stopped) return
    this.stopped = true
    this.socket?.close()
    this.socket = undefined
  }

  private fail(error: Error) {
    if (this.stopped) return
    this.stop()
    this.onError(error)
  }
}
