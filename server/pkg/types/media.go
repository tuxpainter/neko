package types

type MediaWebSocketManager interface {
	Shutdown() error
	Upgrade(checkOrigin CheckOrigin) RouterHandler
}
