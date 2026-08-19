package types

type MediaWebSocketManager interface {
	Upgrade(checkOrigin CheckOrigin) RouterHandler
}
