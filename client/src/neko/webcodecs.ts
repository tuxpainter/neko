import { MediaConfiguration } from './messages'
import { EncodedMediaSample } from './media-protocol'
import { MediaTransport } from './media-transport'
import { MediaWebSocket } from './media-websocket'

const MAX_VIDEO_DECODE_QUEUE_SIZE = 4
const MAX_AUDIO_DECODE_QUEUE_SIZE = 9

export class WebCodecsPlayer {
  private transport?: MediaTransport
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
      config.protocol !== 'webcodecs-ws-v1' ||
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

    this.transport = new MediaWebSocket(this.config.url, this.onSample, this.fail)
    await this.transport.start()
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
    this.transport?.stop()
    this.transport = undefined
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

  private onSample = (sample: EncodedMediaSample) => {
    try {
      if (sample.track === 'video') {
        if (!this.haveVideoKeyframe && !sample.keyframe) return
        if (sample.keyframe) this.haveVideoKeyframe = true
        if ((this.videoDecoder?.decodeQueueSize ?? 0) >= MAX_VIDEO_DECODE_QUEUE_SIZE) {
          this.fail(new Error('media video decoder queue overflow'))
          return
        }
        this.videoDecoder?.decode(
          new EncodedVideoChunk({
            type: sample.keyframe ? 'key' : 'delta',
            timestamp: sample.timestamp,
            duration: sample.duration,
            data: sample.data,
            transfer: [sample.data.buffer as ArrayBuffer],
          }),
        )
      } else {
        if ((this.audioDecoder?.decodeQueueSize ?? 0) >= MAX_AUDIO_DECODE_QUEUE_SIZE) {
          this.fail(new Error('media audio decoder queue overflow'))
          return
        }
        this.audioDecoder?.decode(
          new EncodedAudioChunk({
            type: 'key',
            timestamp: sample.timestamp,
            duration: sample.duration,
            data: sample.data,
            transfer: [sample.data.buffer as ArrayBuffer],
          }),
        )
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
