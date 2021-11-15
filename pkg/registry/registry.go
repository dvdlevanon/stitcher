package registry

import (
	"sync"

	"github.com/dvdlevanon/stitcher/pkg/model"
	"github.com/go-errors/errors"
)

type TaskBuilder interface {
	Build(name string, parent model.Directory) (model.Task, error)
}

type taskRegistry struct {
	types map[string]TaskBuilder
	lock  sync.Mutex
}

var globalRegistry = taskRegistry{
	types: make(map[string]TaskBuilder),
}

func Register(name string, tasktype TaskBuilder) error {
	return globalRegistry.register(name, tasktype)
}

func Build(tasktype string, name string, parent model.Directory) (model.Task, error) {
	return globalRegistry.build(tasktype, name, parent)
}

func (r *taskRegistry) register(name string, tasktype TaskBuilder) error {
	r.lock.Lock()
	defer r.lock.Unlock()

	if _, ok := r.types[name]; ok {
		return errors.Errorf("Task already exists %v", name)
	}

	r.types[name] = tasktype
	return nil
}

func (r *taskRegistry) build(tasktype string, name string, parent model.Directory) (model.Task, error) {
	builder, ok := r.types[tasktype]

	if !ok {
		return nil, errors.Errorf("Unknown task type %v", tasktype)
	}

	task, err := builder.Build(name, parent)

	if err != nil {
		return nil, errors.Wrap(err, 0)
	}

	return task, err
}
