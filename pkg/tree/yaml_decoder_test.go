package tree

import (
	"os"
	"testing"

	"github.com/dvdlevanon/stitcher/pkg/model"
	"github.com/dvdlevanon/stitcher/pkg/registry"
)

var sampleYaml = `
envs: 
  key: val
./tasks: 
  envs.test: 
    key: val
`

type EnvBuilder struct {
}

func (b EnvBuilder) Build(name string, parent model.Directory) (model.Task, error) {
	return nil, nil
}

func TestYaml(t *testing.T) {
	file, err := os.CreateTemp("", "stitcher-test.yaml")

	if err != nil {
		t.Error(err)
	}

	file.WriteString(sampleYaml)
	file.Close()

	registry.Register("envs", EnvBuilder{})

	_, err = DecodeFile(file.Name())

	if err != nil {
		t.Error(err)
	}
}
