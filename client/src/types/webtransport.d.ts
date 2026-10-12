interface WebTransportHash {
  algorithm: 'sha-256'
  value: ArrayBufferView
}

interface WebTransportOptions {
  serverCertificateHashes?: WebTransportHash[]
}

declare class WebTransport {
  constructor(url: string, options?: WebTransportOptions)
  readonly ready: Promise<void>
  readonly closed: Promise<void>
  readonly incomingUnidirectionalStreams: ReadableStream<ReadableStream<Uint8Array>>
  close(): void
}
