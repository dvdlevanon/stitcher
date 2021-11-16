package model

type Directory interface {
	GetName() string
	GetParent() Directory
	GetChildren() []Directory
	GetTasks() []Task
}

type Task interface {
	GetName() string
	GetParent() Directory
	GetType() string
	GetId() string
}
