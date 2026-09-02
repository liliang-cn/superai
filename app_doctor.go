package main

import (
	"context"

	"github.com/liliang-cn/superai-desktop/backend"
)

// Doctor inspects this install and reports what it found.
//
// It deliberately does not go through a.svc: the question it answers is "is
// this machine set up correctly", and the commonest reason to ask it is that
// the service failed to build. A check that needs the thing it is checking
// would be silent exactly when it is wanted.
func (a *App) Doctor() backend.DoctorReport {
	return backend.RunDoctor(context.Background())
}
