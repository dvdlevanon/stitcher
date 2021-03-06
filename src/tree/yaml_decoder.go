package tree

import (
	"bytes"
	"io/ioutil"
	"strings"

	"github.com/dvdlevanon/stitcher/src/model"
	"github.com/dvdlevanon/stitcher/src/registry"
	"github.com/go-errors/errors"
	"gopkg.in/yaml.v2"
)

type directory struct {
	parent   *directory
	name     string
	children map[string]*directory
	tasks    map[string]model.Task
}

func newDirectory(name string, parent *directory) *directory {
	return &directory{
		name:     name,
		parent:   parent,
		children: make(map[string]*directory),
		tasks:    make(map[string]model.Task),
	}
}

func (d *directory) GetName() string {
	return d.name
}

func (d *directory) GetParent() model.Directory {
	return d.parent
}

func (d *directory) GetChildren() []model.Directory {
	result := make([]model.Directory, len(d.children))
	index := 0

	for _, child := range d.children {
		result[index] = child
		index = index + 1
	}

	return result
}

func (d *directory) GetTasks() []model.Task {
	result := make([]model.Task, len(d.tasks))
	index := 0

	for _, task := range d.tasks {
		result[index] = task
		index = index + 1
	}

	return result
}

func DecodeFile(file string) (*directory, error) {
	bytes, err := ioutil.ReadFile(file)

	if err != nil {
		return nil, errors.Wrap(err, 0)
	}

	return DecodeBytes(bytes)
}

func DecodeBytes(bytes []byte) (*directory, error) {
	y := make(map[interface{}]interface{})
	err := yaml.Unmarshal(bytes, y)

	if err != nil {
		return nil, errors.Wrap(err, 0)
	}

	return DecodeYaml(y)
}

func DecodeYaml(y map[interface{}]interface{}) (*directory, error) {
	root := newDirectory("", nil)

	if err := root.decodeYaml(y); err != nil {
		return nil, err
	}

	return root, nil
}

func (d *directory) decodeYaml(y map[interface{}]interface{}) error {
	for keyobj, value := range y {
		key, ok := keyobj.(string)

		if !ok {
			return errors.Errorf("Key is not a string - %v", key)
		}

		if strings.HasPrefix(key, "./") {
			if err := d.decodeDirectory(key[2:], value); err != nil {
				return err
			}
		} else {
			if err := d.decodeTask(key, value); err != nil {
				return err
			}
		}
	}

	return nil
}

func (d *directory) decodeDirectory(name string, value interface{}) error {
	newdir := newDirectory(name, d)
	d.children[name] = newdir

	if value == nil {
		return nil
	}

	valuemap, ok := (value).(map[interface{}]interface{})

	if !ok {
		return errors.Errorf("Value is not a map - %T", value)
	}

	if err := newdir.decodeYaml(valuemap); err != nil {
		return errors.Wrap(err, 0)
	}

	return nil
}

func (d *directory) decodeTask(id string, value interface{}) error {
	tasktype, name, err := parseTaskId(id)

	if err != nil {
		return err
	}

	task, err := d.buildTask(tasktype, name)

	if err != nil {
		return err
	}

	if err := unmarshalTask(task, value); err != nil {
		return err
	}

	d.tasks[task.GetId()] = task
	return nil
}

func parseTaskId(id string) (string, string, error) {
	if len(id) == 0 {
		return "", "", errors.Errorf("Empty task id")
	}

	parts := strings.Split(id, ".")

	if len(parts) < 1 || len(parts) > 2 {
		return "", "", errors.Errorf("Invalid task id %v", id)
	}

	tasktype := parts[0]
	name := ""

	if len(parts) > 1 {
		name = parts[1]
	}

	return tasktype, name, nil
}

func (d *directory) buildTask(tasktype string, name string) (model.Task, error) {
	task, err := registry.Build(tasktype, name, d)

	if err != nil {
		return nil, errors.Wrap(err, 0)
	}

	if task == nil {
		return nil, errors.Errorf("Created task is null: %v", tasktype)
	}

	if task.GetParent() != d {
		return nil, errors.Errorf("Task created with invalid parent %v != %v", d, task.GetParent())
	}

	if task.GetName() != name {
		return nil, errors.Errorf("Task created with invalid name %v != %v", name, task.GetName())
	}

	return task, nil
}

func unmarshalTask(task model.Task, value interface{}) error {
	marshalledtask, err := yaml.Marshal(value)

	if err != nil {
		return errors.Wrap(err, 0)
	}

	decoder := yaml.NewDecoder(bytes.NewReader(marshalledtask))

	if err := decoder.Decode(&task); err != nil {
		return err
	}

	return nil
}
