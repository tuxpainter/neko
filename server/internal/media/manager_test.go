package media

import (
	"testing"
	"time"
)

func TestConsumeTicketRejectsDifferentTransport(t *testing.T) {
	manager := &Manager{
		tickets: map[string]Ticket{
			"token": {transport: TransportWebTransport, expires: time.Now().Add(time.Minute)},
		},
	}

	if _, _, err := manager.ConsumeTicket("token", TransportWebSocket); err == nil {
		t.Fatal("expected transport mismatch error")
	}
	if _, ok := manager.tickets["token"]; ok {
		t.Fatal("ticket was not consumed")
	}
}
