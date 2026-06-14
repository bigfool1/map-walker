package synthetic

const (
	saltActivation uint64 = 1
	saltFirstInput uint64 = 2
	saltDirection  uint64 = 3
	saltInterval   uint64 = 4
)

func behaviorRoll(accountNumber int, seed uint64, index int, salt uint64) uint64 {
	v := uint64(accountNumber)*0x9E3779B97F4A7C15 +
		seed*0xBF58476D1CE4E5B9 +
		uint64(index)*0xff51afd7ed558ccd +
		salt
	v ^= v >> 33
	v *= 0xc4ceb9fe1a85ec53
	v ^= v >> 33
	return v
}
