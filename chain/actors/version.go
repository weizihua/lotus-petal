package actors

import (
	"fmt"

	"github.com/filecoin-project/go-state-types/network"
)

type Version int

const (
	Version0 = 0
	Version2 = 2
)

// Converts a network version into an actors adt version.
func VersionForNetwork(version network.Version) Version {
	switch version {
	case network.Version0, network.Version1, network.Version2, network.Version3:
		return Version0
	case network.Version4:
		return Version2
	default:
		panic(fmt.Sprintf("unsupported network version %d", version))
	}
}
