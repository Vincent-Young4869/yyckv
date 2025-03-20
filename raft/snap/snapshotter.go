package snap

import "go.uber.org/zap"

type Snapshotter struct {
	lg  *zap.Logger
	dir string
}

func New(lg *zap.Logger, dir string) *Snapshotter {
	if lg == nil {
		lg = zap.NewNop()
	}
	return &Snapshotter{
		lg:  lg,
		dir: dir,
	}
}
