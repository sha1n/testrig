package testrig

import (
	"fmt"

	"github.com/sha1n/testrig/api"
)

// Stages describes an ordered sequence of stages for service startup.
// Within a stage, services start concurrently; stages run sequentially
// in declaration order. Pass to Env.WithStages to register the sequence
// as one of the env's tracks.
//
// On Stop, stages within a track tear down in reverse order (last stage
// first); services within a stage stop concurrently.
type Stages struct {
	stages [][]api.Service
}

// NewStages starts a sequence with one stage containing the given
// services. Use Then to add subsequent stages. Panics if any service is
// nil.
func NewStages(services ...api.Service) *Stages {
	for i, s := range services {
		if s == nil {
			panic(fmt.Sprintf("testrig: NewStages received a nil Service at index %d", i))
		}
	}
	return &Stages{stages: [][]api.Service{services}}
}

// Then appends a new stage containing the given services. The new stage
// will start only after the previous stage's services have all started
// successfully. Panics if any service is nil.
func (s *Stages) Then(services ...api.Service) *Stages {
	for i, svc := range services {
		if svc == nil {
			panic(fmt.Sprintf("testrig: Stages.Then received a nil Service at index %d", i))
		}
	}
	s.stages = append(s.stages, services)
	return s
}

// singleStage builds a Stages with exactly one stage. Used internally by
// Env.With as the single-stage shortcut.
func singleStage(services []api.Service) *Stages {
	return &Stages{stages: [][]api.Service{services}}
}
