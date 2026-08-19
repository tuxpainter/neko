const HEADER_SIZE = 20
const PROTOCOL_VERSION = 1
const VIDEO_TRACK = 1
const AUDIO_TRACK = 2

export interface EncodedMediaSample {
  track: 'video' | 'audio'
  timestamp: number
  duration?: number
  keyframe: boolean
  data: Uint8Array
}

export function parseMediaSample(data: ArrayBuffer): EncodedMediaSample {
  if (data.byteLength < HEADER_SIZE) throw new Error('invalid media websocket message')

  const view = new DataView(data)
  if (view.getUint8(0) !== PROTOCOL_VERSION) {
    throw new Error('unsupported media websocket protocol version')
  }

  const trackID = view.getUint8(1)
  let track: EncodedMediaSample['track']
  if (trackID === VIDEO_TRACK) track = 'video'
  else if (trackID === AUDIO_TRACK) track = 'audio'
  else throw new Error('unknown media track')

  const rawDuration = Number(view.getBigInt64(12))
  return {
    track,
    timestamp: Number(view.getBigInt64(4)),
    duration: rawDuration >= 0 ? rawDuration : undefined,
    keyframe: (view.getUint8(2) & 1) !== 0,
    data: new Uint8Array(data, HEADER_SIZE),
  }
}
