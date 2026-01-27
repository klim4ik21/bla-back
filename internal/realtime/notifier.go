package realtime

import "github.com/google/uuid"

// Notifier wraps Node for easy use in handlers
type Notifier struct {
	node *Node
}

func NewNotifier(node *Node) *Notifier {
	return &Notifier{node: node}
}

func (n *Notifier) NotifyUser(userID uuid.UUID, eventType string, data interface{}) error {
	return n.node.PublishToUser(userID, eventType, data)
}

func (n *Notifier) NotifyUsers(userIDs []uuid.UUID, eventType string, data interface{}) {
	n.node.PublishToUsers(userIDs, eventType, data)
}
