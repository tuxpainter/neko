import { MediaConfiguration } from './messages'

const HEADER_SIZE = 20
const VIDEO_TRACK = 1
const AUDIO_TRACK = 2

export class WebCodecsPlayer {
  private socket?: WebSocket
  private videoDecoder?: VideoDecoder
  private audioDecoder?: AudioDecoder
  private audioContext?: AudioContext
  private gain?: GainNode
  private audioCursor = 0
  private playing = false
  private muted = false
  private volume = 1
  private ready = false
  private rendered = false
  private stopped = false
  private haveVideoKeyframe = false

  constructor(
    private readonly canvas: HTMLCanvasElement,
    private readonly config: MediaConfiguration,
    private readonly onReady: () => void,
    private readonly onError: (error: Error) => void,
  ) {}

  static async supported(config: MediaConfiguration) {
    if (
      config.protocol !== 'webcodecs-v1' ||
      typeof VideoDecoder === 'undefined' ||
      typeof AudioDecoder === 'undefined'
    ) {
      return false
    }
    const [video, audio] = await Promise.all([
      VideoDecoder.isConfigSupported({
        codec: config.video.codec,
        hardwareAcceleration: 'no-preference',
        optimizeForLatency: true,
      }),
      AudioDecoder.isConfigSupported({
        codec: config.audio.codec,
        sampleRate: config.audio.sample_rate,
        numberOfChannels: config.audio.number_of_channels,
      }),
    ])
    return video.supported === true && audio.supported === true
  }

  async start() {
    if (!(await WebCodecsPlayer.supported(this.config))) {
      throw new Error('browser does not support the configured WebCodecs streams')
    }

    this.videoDecoder = new VideoDecoder({ output: this.onVideoFrame, error: this.fail })
    this.videoDecoder.configure({
      codec: this.config.video.codec,
      hardwareAcceleration: 'no-preference',
      optimizeForLatency: true,
    })
    this.audioDecoder = new AudioDecoder({ output: this.onAudioData, error: this.fail })
    this.audioDecoder.configure({
      codec: this.config.audio.codec,
      sampleRate: this.config.audio.sample_rate,
      numberOfChannels: this.config.audio.number_of_channels,
    })

    window.addEventListener('pointerdown', this.activateAudio, true)
    window.addEventListener('keydown', this.activateAudio, true)

    this.socket = new WebSocket(this.config.url)
    this.socket.binaryType = 'arraybuffer'
    this.socket.onmessage = ({ data }) => this.onMessage(data)
    this.socket.onerror = () => this.fail(new Error('media websocket failed'))
    this.socket.onclose = () => {
      if (!this.stopped) this.fail(new Error('media websocket closed'))
    }
  }

  async setPlaying(playing: boolean) {
    this.playing = playing
    this.audioCursor = 0
    if (playing && this.audioContext) {
      await this.audioContext?.resume()
    }
  }

  setVolume(volume: number) {
    this.volume = volume
    this.updateGain()
  }

  setMuted(muted: boolean) {
    this.muted = muted
    this.updateGain()
  }

  stop() {
    if (this.stopped) return
    this.stopped = true
    window.removeEventListener('pointerdown', this.activateAudio, true)
    window.removeEventListener('keydown', this.activateAudio, true)
    this.socket?.close()
    this.socket = undefined
    try {
      this.videoDecoder?.close()
    } catch (_) {}
    this.videoDecoder = undefined
    try {
      this.audioDecoder?.close()
    } catch (_) {}
    this.audioDecoder = undefined
    this.gain?.disconnect()
    this.gain = undefined
    void this.audioContext?.close()
    this.audioContext = undefined
  }

  private onMessage(data: unknown) {
    if (!(data instanceof ArrayBuffer) || data.byteLength < HEADER_SIZE) {
      this.fail(new Error('invalid media websocket message'))
      return
    }
    const view = new DataView(data)
    if (view.getUint8(0) !== 1) {
      this.fail(new Error('unsupported media websocket protocol version'))
      return
    }
    const track = view.getUint8(1)
    const keyframe = (view.getUint8(2) & 1) !== 0
    const timestamp = Number(view.getBigInt64(4))
    const rawDuration = Number(view.getBigInt64(12))
    const duration = rawDuration >= 0 ? rawDuration : undefined
    const payload = new Uint8Array(data, HEADER_SIZE)

    try {
      if (track === VIDEO_TRACK) {
        if (!this.haveVideoKeyframe && !keyframe) return
        if (keyframe) this.haveVideoKeyframe = true
        if (!keyframe && (this.videoDecoder?.decodeQueueSize ?? 0) > 3) return
        this.videoDecoder?.decode(
          new EncodedVideoChunk({ type: keyframe ? 'key' : 'delta', timestamp, duration, data: payload }),
        )
      } else if (track === AUDIO_TRACK) {
        if ((this.audioDecoder?.decodeQueueSize ?? 0) > 8) return
        this.audioDecoder?.decode(new EncodedAudioChunk({ type: 'key', timestamp, duration, data: payload }))
      } else {
        throw new Error('unknown media track')
      }
    } catch (error) {
      this.fail(error instanceof Error ? error : new Error(String(error)))
    }
  }

  private onVideoFrame = (frame: VideoFrame) => {
    try {
      if (!this.ready) {
        this.ready = true
        this.onReady()
      }
      if (!this.playing && this.rendered) return
      if (this.canvas.width !== frame.displayWidth || this.canvas.height !== frame.displayHeight) {
        this.canvas.width = frame.displayWidth
        this.canvas.height = frame.displayHeight
      }
      const context = this.canvas.getContext('2d', { alpha: false })
      context?.drawImage(frame as unknown as CanvasImageSource, 0, 0, this.canvas.width, this.canvas.height)
      this.rendered = true
    } finally {
      frame.close()
    }
  }

  private onAudioData = (data: AudioData) => {
    try {
      if (!this.playing || !this.audioContext || !this.gain) return
      const buffer = this.audioContext.createBuffer(data.numberOfChannels, data.numberOfFrames, data.sampleRate)
      for (let channel = 0; channel < data.numberOfChannels; channel++) {
        data.copyTo(buffer.getChannelData(channel), { planeIndex: channel, format: 'f32-planar' })
      }
      const now = this.audioContext.currentTime
      if (this.audioCursor < now || this.audioCursor > now + 0.15) this.audioCursor = now + 0.03
      const source = this.audioContext.createBufferSource()
      source.buffer = buffer
      source.connect(this.gain)
      source.start(this.audioCursor)
      this.audioCursor += buffer.duration
    } finally {
      data.close()
    }
  }

  private updateGain() {
    if (this.gain) this.gain.gain.value = this.muted ? 0 : this.volume
  }

  private ensureAudio() {
    if (this.audioContext) return
    this.audioContext = new AudioContext({ latencyHint: 'interactive', sampleRate: this.config.audio.sample_rate })
    this.gain = this.audioContext.createGain()
    this.updateGain()
    this.gain.connect(this.audioContext.destination)
  }

  private activateAudio = () => {
    this.ensureAudio()
    void this.audioContext?.resume()
    window.removeEventListener('pointerdown', this.activateAudio, true)
    window.removeEventListener('keydown', this.activateAudio, true)
  }

  private fail = (error: Error | DOMException) => {
    if (this.stopped) return
    this.stop()
    this.onError(error instanceof Error ? error : new Error(String(error)))
  }
}
