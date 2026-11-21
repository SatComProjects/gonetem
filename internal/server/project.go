package server

import "time"

type ProjectNotFoundError struct {
	Id string
}

func (e *ProjectNotFoundError) Error() string {
	return "Project " + e.Id + ": not found"
}

type NetemProject struct {
	Id       string
	Name     string
	Dir      string
	OpenAt   time.Time
	Topology *NetemTopologyManager
}
