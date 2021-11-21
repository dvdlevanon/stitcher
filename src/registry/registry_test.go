package registry

import (
	"fmt"
	"testing"

	"github.com/dvdlevanon/stitcher/src/mocks"
	"github.com/go-errors/errors"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/require"
)

//go:generate /home/david/temp/temp/2021-11-16-223313/mock_1.6.0_linux_amd64/mockgen -destination=../mocks/structs_mocks.go -source ../model/structs.go -package mocks
//go:generate /home/david/temp/temp/2021-11-16-223313/mock_1.6.0_linux_amd64/mockgen -destination=../mocks/registry_mocks.go -source ../registry/registry.go -package mocks

func TestSingleTask(t *testing.T) {
	ctrl := gomock.NewController(t)
	builder := mocks.NewMockTaskBuilder(ctrl)
	tasktype := "test"

	err := Register(tasktype, builder)
	require.NoError(t, err)

	name := "task1"
	parent := mocks.NewMockDirectory(ctrl)
	task := mocks.NewMockTask(ctrl)
	task.EXPECT().GetName().Return(name)
	task.EXPECT().GetParent().Return(parent)
	builder.EXPECT().Build(name, parent).Return(task, nil)

	createdTask, err := Build(tasktype, name, parent)

	require.NoError(t, err)
	require.Equal(t, task, createdTask)
	require.Equal(t, task.GetParent(), parent)
	require.Equal(t, task.GetName(), name)
}

func TestMutipleTasks(t *testing.T) {
	ctrl := gomock.NewController(t)

	for i := 1; i < 5; i++ {
		builder := mocks.NewMockTaskBuilder(ctrl)
		tasktype := fmt.Sprintf("test-%v", i)

		err := Register(tasktype, builder)
		require.NoError(t, err)

		name := fmt.Sprintf("task1-%v", i)
		parent := mocks.NewMockDirectory(ctrl)
		task := mocks.NewMockTask(ctrl)
		task.EXPECT().GetName().Return(name)
		task.EXPECT().GetParent().Return(parent)
		builder.EXPECT().Build(name, parent).Return(task, nil)

		createdTask, err := Build(tasktype, name, parent)

		require.NoError(t, err)
		require.Equal(t, task, createdTask)
		require.Equal(t, task.GetParent(), parent)
		require.Equal(t, task.GetName(), name)
	}
}

func TestMissingTask(t *testing.T) {
	_, err := Build("missing-task", "", nil)
	require.Error(t, err)
}

func TestDuplicateNames(t *testing.T) {
	ctrl := gomock.NewController(t)
	builder := mocks.NewMockTaskBuilder(ctrl)
	err := Register("task1", builder)
	require.NoError(t, err)
	err = Register("task1", builder)
	require.Error(t, err)
}

func TestNilBuilder(t *testing.T) {
	err := Register("task", nil)
	require.Error(t, err)
}

func TestEmptyTaskName(t *testing.T) {
	ctrl := gomock.NewController(t)
	builder := mocks.NewMockTaskBuilder(ctrl)
	err := Register("", builder)
	require.Error(t, err)
}

func TestBuilderError(t *testing.T) {
	ctrl := gomock.NewController(t)
	builder := mocks.NewMockTaskBuilder(ctrl)

	builder.EXPECT().Build("", nil).Return(nil, errors.Errorf("for testing"))

	err := Register("test-builder-error", builder)
	require.NoError(t, err)
	_, err = Build("test-builder-error", "", nil)
	require.Error(t, err)
}
