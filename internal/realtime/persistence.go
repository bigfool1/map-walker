package realtime

type PositionUpdate struct {
	UserID string
	Lat    float64
	Lng    float64
	Seq    uint64
}

type PositionPersister interface {
	Submit(updates []PositionUpdate)
	Stop()
}

type PositionDrainer interface {
	PositionPersister
	Drain()
}
