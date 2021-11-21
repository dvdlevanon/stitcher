package registry

import (
	"sync"

	"github.com/dvdlevanon/stitcher/src/model"
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

func Register(tasktype string, builder TaskBuilder) error {
	return globalRegistry.register(tasktype, builder)
}

func Build(tasktype string, name string, parent model.Directory) (model.Task, error) {
	return globalRegistry.build(tasktype, name, parent)
}

func (r *taskRegistry) register(tasktype string, builder TaskBuilder) error {
	if tasktype == "" {
		return errors.Errorf("Task type is empty")
	}

	if builder == nil {
		return errors.Errorf("Task builder is nil")
	}

	r.lock.Lock()
	defer r.lock.Unlock()

	if _, ok := r.types[tasktype]; ok {
		return errors.Errorf("Task already exists %v", tasktype)
	}

	r.types[tasktype] = builder
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
