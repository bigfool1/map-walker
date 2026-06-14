package realtime

import "time"

// HubSnapshot is an immutable point-in-time aggregate of the most recently
// completed one-second Hub stats interval. Reads are safe from any goroutine.
type HubSnapshot struct {
	ConnectedClients      int
	AcceptedInputs        uint64
	SimulationTicks       uint64
	MovedPlayers          uint64
	AOICandidatePairs     uint64
	AOIDistanceChecks     uint64
	RelationshipsEntered  uint64
	RelationshipsLeft     uint64
	ReplicationMessages   uint64
	ReplicationRecipients uint64
	ReplicationBytes      uint64
	SampledAt             time.Time
}
