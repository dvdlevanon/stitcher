package main

import (
	"bufio"
	"bytes"
	gocontext "context"
	"encoding/base64"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"text/template"

	execute "github.com/alexellis/go-execute/pkg/v1"
	"github.com/go-errors/errors"
	"github.com/hashicorp/terraform-exec/tfexec"
	"github.com/stevenle/topsort"
	"gopkg.in/yaml.v2"
)

type Context struct {
	outputs               map[string]*TaskOutput
	workingDirectoryStack []string
}

func (c *Context) getWorkingDirectory() string {
	return c.workingDirectoryStack[len(c.workingDirectoryStack)-1]
}

func (c *Context) pushWorkingDirectory(task Task) error {
	dir := task.getWorkingDirectory()

	if !strings.HasPrefix(dir, "/") {
		dir = filepath.Join(c.getWorkingDirectory(), dir)
	}

	stat, err := os.Stat(dir)

	if os.IsNotExist(err) || !stat.IsDir() {
		base := filepath.Base(dir)

		if base == task.getName() {
			dir = filepath.Dir(dir)
		}
	}

	_, err = os.Stat(dir)

	if os.IsNotExist(err) {
		return errors.Errorf("Directory not found %v", dir)
	}

	c.workingDirectoryStack = append(c.workingDirectoryStack, dir)

	if err := os.Chdir(c.getWorkingDirectory()); err != nil {
		return errors.Wrap(err, 0)
	}

	fmt.Printf("Pushing current dir: %v\n", c.getWorkingDirectory())
	return nil
}

func (c *Context) popWorkingDirectory() error {
	c.workingDirectoryStack = c.workingDirectoryStack[:len(c.workingDirectoryStack)-1]

	if err := os.Chdir(c.getWorkingDirectory()); err != nil {
		return errors.Wrap(err, 0)
	}

	fmt.Printf("Popping current dir: %v\n", c.getWorkingDirectory())
	return nil
}

type TaskInput struct {
	vars map[string]string
}

type TaskOutput struct {
	vars map[string]string
}

type Task interface {
	getType() string
	getName() string
	setName(name string)
	setParent(parent *Directory) error
	getParent() *Directory
	getPath(tasktype string) string
	getWorkingDirectory() string
	getId(tasktype string) string
	run(context *Context, input *TaskInput) (*TaskOutput, error)
	getExpressions() []string
	TaskByUri(uri string) (Task, error)
}

type CommonTask struct {
	parent           *Directory
	name             string
	WorkingDirectory string `yaml:"directory"`
}

func (t *CommonTask) setParent(parent *Directory) error {
	if t.parent != nil {
		return errors.Errorf("Task already belong to another parent %v", t)
	}

	t.parent = parent
	return nil
}

func (t *CommonTask) getParent() *Directory {
	return t.parent
}

func (t *CommonTask) setName(name string) {
	t.name = name
}

func (t *CommonTask) getName() string {
	return t.name
}

func (t *CommonTask) getPath(tasktype string) string {
	path := t.parent.GetPath()

	if path == "" {
		return t.getId(tasktype)
	} else {
		return path + "." + t.getId(tasktype)
	}
}

func (t *CommonTask) getWorkingDirectory() string {
	if t.WorkingDirectory != "" && strings.HasPrefix(t.WorkingDirectory, "/") {
		return t.WorkingDirectory
	}

	dir := t.getParent().getWorkingDirectory()

	if t.WorkingDirectory != "" {
		return filepath.Join(dir, t.WorkingDirectory)
	} else {
		return filepath.Join(dir, t.name)
	}
}

func (t *CommonTask) getId(tasktype string) string {
	if t.name == "" {
		return tasktype
	} else {
		return tasktype + "." + t.name
	}
}

func (t *CommonTask) TaskByUri(uri string) (Task, error) {
	if t.parent == nil {
		return nil, errors.Errorf("Task is not mounted %v", t)
	}

	return t.parent.TaskByUri(uri)
}

type Executable struct {
	Envs map[string]string `yaml:"envs"`
	Args []string          `yaml:"args"`
}

func (e *Executable) getExpressions() []string {
	result := make([]string, 0)
	result = append(result, e.Args...)

	for _, value := range e.Envs {
		result = append(result, value)
	}

	return result
}

func (e *Executable) EvaluateAndRun(exec string, input *TaskInput) (*execute.ExecResult, error) {
	envs := make([]string, 0)

	for key, val := range e.Envs {
		expr := Expression{
			Expr: val,
		}

		evaluated, err := expr.Evaluate(input)

		if err != nil {
			return nil, errors.Wrap(err, 0)
		}

		envs = append(envs, key+"="+evaluated)
	}

	args := make([]string, 0)

	for _, arg := range e.Args {
		expr := Expression{Expr: arg}
		evaluated, err := expr.Evaluate(input)

		if err != nil {
			return nil, errors.Wrap(err, 0)
		}

		args = append(args, evaluated)
	}

	return e.internalRun(exec, envs, args, true)
}

func (e *Executable) Run(exec string, streamStdio bool) (*execute.ExecResult, error) {
	envs := make([]string, 0)

	for key, val := range e.Envs {
		envs = append(envs, key+"="+val)
	}

	return e.internalRun(exec, envs, e.Args, streamStdio)
}

func (e *Executable) internalRun(exec string, envs []string, args []string, streamStdio bool) (*execute.ExecResult, error) {
	cmd := execute.ExecTask{
		Command:      exec,
		Env:          envs,
		Args:         args,
		Stdin:        os.Stdin,
		StreamStdio:  streamStdio,
		PrintCommand: false,
	}

	result, err := cmd.Execute()

	if err != nil {
		return nil, errors.Wrap(err, 0)
	}

	if result.ExitCode != 0 {
		return nil, errors.Errorf("Error code is not zero %v %v %v", exec, args, result.ExitCode)
	}

	return &result, nil
}

type ScriptTask struct {
	CommonTask `yaml:",inline"`
	Executable `yaml:",inline"`
	File       string
}

func (t *ScriptTask) getExpressions() []string {
	return append(t.Executable.getExpressions(), t.File)
}

func (t *ScriptTask) getType() string {
	return "script"
}

func (t *ScriptTask) run(context *Context, input *TaskInput) (*TaskOutput, error) {
	t.Args = append([]string{t.File}, t.Args...)
	result, err := t.Executable.EvaluateAndRun("/bin/bash", input)

	if err != nil {
		return nil, errors.Wrap(err, 0)
	}

	return &TaskOutput{
		vars: map[string]string{
			"Stdout":   result.Stdout,
			"Stderr":   result.Stderr,
			"ExitCode": strconv.Itoa(result.ExitCode),
		},
	}, nil
}

type PropertiesTask struct {
	CommonTask `yaml:",inline"`
	Files      []string `yaml:"files"`
}

func (t *PropertiesTask) getExpressions() []string {
	return t.Files
}

func (t *PropertiesTask) getType() string {
	return "properties"
}

func (t *PropertiesTask) run(context *Context, input *TaskInput) (*TaskOutput, error) {
	vars := make(map[string]string)

	for _, path := range t.Files {
		expr := Expression{
			Expr: path,
		}

		evaluated, err := expr.Evaluate(input)

		if err != nil {
			return nil, errors.Wrap(err, 0)
		}

		file, err := os.Open(evaluated)

		if err != nil {
			return nil, errors.Wrap(err, 0)
		}

		defer file.Close()

		scanner := bufio.NewScanner(file)

		for scanner.Scan() {
			line := scanner.Text()

			if strings.TrimSpace(line) == "" {
				continue
			}

			if strings.HasPrefix(strings.TrimSpace(line), "#") {
				continue
			}

			if strings.HasPrefix(strings.TrimSpace(line), "export ") {
				line = line[len("export "):]
			}

			equalIndex := strings.Index(line, "=")

			if equalIndex == -1 {
				return nil, errors.Errorf("Invalid line %v in properties file %v", line, evaluated)
			}

			key := line[0:equalIndex]
			value := line[equalIndex+1:]

			vars[key] = value
		}

		if err := scanner.Err(); err != nil {
			return nil, errors.Wrap(err, 0)
		}
	}

	return &TaskOutput{
		vars: vars,
	}, nil
}

type VarsTask struct {
	CommonTask `yaml:",inline"`
	Vars       map[string]string `yaml:",inline"`
}

func (e *VarsTask) getExpressions() []string {
	result := make([]string, 0)

	for _, value := range e.Vars {
		result = append(result, value)
	}

	return result
}

func (e *VarsTask) getType() string {
	return "vars"
}

func (e *VarsTask) run(context *Context, input *TaskInput) (*TaskOutput, error) {
	vars := make(map[string]string)

	for key, val := range e.Vars {

		expr := Expression{
			Expr: val,
		}

		evaluated, err := expr.Evaluate(input)

		if err != nil {
			return nil, errors.Wrap(err, 0)
		}

		vars[key] = evaluated
	}

	return &TaskOutput{
		vars: vars,
	}, nil
}

type EnvsTask struct {
	CommonTask `yaml:",inline"`
	Envs       map[string]string `yaml:",inline"`
}

func (e *EnvsTask) getExpressions() []string {
	result := make([]string, 0)

	for _, value := range e.Envs {
		result = append(result, value)
	}

	return result
}

func (e *EnvsTask) getType() string {
	return "envs"
}

func (e *EnvsTask) run(context *Context, input *TaskInput) (*TaskOutput, error) {
	vars := make(map[string]string)

	if e.name == "" {
		for _, env := range os.Environ() {
			pair := strings.SplitN(env, "=", 2)
			vars[pair[0]] = pair[1]
		}
	}

	return &TaskOutput{
		vars: vars,
	}, nil
}

type TerraformInit struct {
	Executable `yaml:",inline"`
}

type TerraformTask struct {
	CommonTask `yaml:",inline"`
	Location   string `yaml:"location"`
	Executable `yaml:",inline"`
	Init       TerraformInit     `yaml:"init"`
	Vars       map[string]string `yaml:"vars"`
}

func (t *TerraformTask) getExpressions() []string {
	result := append(t.Executable.getExpressions(), t.Init.getExpressions()...)

	for _, value := range t.Vars {
		result = append(result, value)
	}

	return append(result, t.Location)
}

func (e *TerraformTask) getType() string {
	return "terraform"
}

func (e *TerraformTask) run(context *Context, input *TaskInput) (*TaskOutput, error) {
	envs := make(map[string]string)

	for key, val := range e.Envs {
		expr := Expression{
			Expr: val,
		}

		evaluated, err := expr.Evaluate(input)

		if err != nil {
			return nil, errors.Wrap(err, 0)
		}

		envs[key] = evaluated
	}

	for _, env := range os.Environ() {
		pair := strings.SplitN(env, "=", 2)
		envs[pair[0]] = pair[1]
	}

	planvars := make([]tfexec.PlanOption, 0)
	applyvars := make([]tfexec.ApplyOption, 0)

	for key, val := range e.Vars {
		expr := Expression{
			Expr: val,
		}

		evaluated, err := expr.Evaluate(input)

		if err != nil {
			return nil, errors.Wrap(err, 0)
		}

		tfvar := fmt.Sprintf("%v=%v", key, evaluated)

		fmt.Printf("%v\n", tfvar)

		planvars = append(planvars, tfexec.Var(tfvar))
		applyvars = append(applyvars, tfexec.Var(tfvar))
	}

	tf, err := tfexec.NewTerraform(context.getWorkingDirectory(), "/usr/local/bin/terraform")

	if err != nil {
		return nil, errors.Wrap(err, 0)
	}

	tf.SetStdout(os.Stdin)
	tf.SetStderr(os.Stderr)

	if err := tf.SetEnv(envs); err != nil {
		return nil, errors.Wrap(err, 0)
	}

	shouldrun, err := tf.Plan(gocontext.TODO(), planvars...)

	if err != nil {
		return nil, errors.Wrap(err, 0)
	}

	if shouldrun {
		reader := bufio.NewReader(os.Stdin)
		fmt.Print(`
Do you want to perform these actions?
  Terraform will perform the actions described above.
  Only 'yes' will be accepted to approve.

  Enter a value: `)

		answer, err := reader.ReadString('\n')

		if err != nil {
			return nil, errors.Wrap(err, 0)
		}

		if strings.TrimSpace(answer) != "yes" {
			return nil, errors.Errorf("Aborted!")
		}

		tf.Apply(gocontext.TODO(), applyvars...)

		if err != nil {
			return nil, errors.Wrap(err, 0)
		}
	}

	terraformOutput, err := tf.Output(gocontext.TODO())

	if err != nil {
		return nil, errors.Wrap(err, 0)
	}

	vars := make(map[string]string)

	for key, val := range terraformOutput {
		valstr := string(val.Value)

		vars[key] = valstr
	}

	return &TaskOutput{
		vars: vars,
	}, nil
}

type AnsiblePlaybookTask struct {
	CommonTask `yaml:",inline"`
	Executable `yaml:",inline"`
	Playbook   string `yaml:"playbook"`
	Inventory  string `yaml:"inventory"`
}

func (a *AnsiblePlaybookTask) getExpressions() []string {
	return append(a.Executable.getExpressions(), a.Playbook, a.Inventory)
}

func (a *AnsiblePlaybookTask) getType() string {
	return "ansible"
}

func (a *AnsiblePlaybookTask) run(context *Context, input *TaskInput) (*TaskOutput, error) {
	return nil, errors.New("Not implemented")
}

type KubernetesTask struct {
	CommonTask `yaml:",inline"`
	Executable `yaml:",inline"`
	File       string `yaml:"file"`
}

func (k *KubernetesTask) getExpressions() []string {
	return append(k.Executable.getExpressions(), k.File)
}

func (k *KubernetesTask) getType() string {
	return "kubernetes"
}

func (k *KubernetesTask) run(context *Context, input *TaskInput) (*TaskOutput, error) {
	return nil, errors.New("Not implemented")
}

type HelmfileTask struct {
	CommonTask `yaml:",inline"`
	Executable `yaml:",inline"`
	File       string `yaml:"file"`
}

func (t *HelmfileTask) getExpressions() []string {
	return append(t.Executable.getExpressions(), t.File)
}

func (t *HelmfileTask) getType() string {
	return "helmfile"
}

func (t *HelmfileTask) run(context *Context, input *TaskInput) (*TaskOutput, error) {
	return nil, errors.New("Not implemented")
}

type Plan struct {
	tasks []Task
}

func BuildPlan(root *Directory, taskNames []string) (*Plan, error) {
	tasks := make(map[string]*TaskDependencies)

	for _, taskname := range taskNames {
		task, err := root.TaskByUri(taskname)

		if err != nil {
			return nil, errors.Wrap(err, 0)
		}

		if err := addTaskWithDependencies(task, tasks); err != nil {
			return nil, errors.Wrap(err, 0)
		}
	}

	graph := topsort.NewGraph()

	for dependent, dependentTask := range tasks {
		for dependee := range dependentTask.Dependencies {
			if err := graph.AddEdge(dependent, dependee); err != nil {
				return nil, errors.Wrap(err, 0)
			}
		}
	}

	alreadyAdded := make(map[string]bool)
	finalTasks := make([]Task, 0)

	for _, taskname := range taskNames {
		task, err := root.TaskByUri(taskname)

		if err != nil {
			return nil, errors.Wrap(err, 0)
		}

		sortedTasks, err := graph.TopSort(task.getPath(task.getType()))

		if err != nil {
			return nil, errors.Wrap(err, 0)
		}

		for _, sortedTaskName := range sortedTasks {
			taskToAdd, ok := tasks[sortedTaskName]

			if !ok {
				return nil, errors.Errorf("Task not found in sorted list %v", sortedTaskName)
			}

			added := alreadyAdded[sortedTaskName]

			if added {
				continue
			}

			finalTasks = append(finalTasks, taskToAdd.Task)
			alreadyAdded[sortedTaskName] = true
		}
	}

	return &Plan{
		tasks: finalTasks,
	}, nil
}

func addTaskWithDependencies(task Task, tasks map[string]*TaskDependencies) error {
	_, found := tasks[task.getPath(task.getType())]

	if found {
		return nil
	}

	taskWithDeps, err := BuildTaskDependencies(task)

	if err != nil {
		return errors.Wrap(err, 0)
	}

	tasks[task.getPath(task.getType())] = taskWithDeps

	for _, dep := range taskWithDeps.Dependencies {
		if err := addTaskWithDependencies(dep, tasks); err != nil {
			return errors.Wrap(err, 0)
		}
	}

	return nil
}

type TaskDependencies struct {
	Task         Task
	Dependencies map[string]Task
}

func BuildTaskDependencies(task Task) (*TaskDependencies, error) {
	dependencies := make(map[string]Task)
	expressions := task.getExpressions()

	for _, expr := range expressions {
		expr := Expression{
			Expr: expr,
		}

		for _, token := range expr.getTokens() {
			dotIndex := strings.LastIndex(token, ".")

			if dotIndex == -1 {
				return nil, errors.Errorf("Invalid reference %v", token)
			}

			path := token[0:dotIndex]

			dependentTask, err := task.TaskByUri(path)

			if err != nil {
				return nil, errors.Wrap(err, 0)
			}

			dependencies[dependentTask.getPath(dependentTask.getType())] = dependentTask
		}
	}

	return &TaskDependencies{
		Task:         task,
		Dependencies: dependencies,
	}, nil
}

func (p *Plan) run(context *Context) error {
	fmt.Printf("Plan started\n")

	for _, task := range p.tasks {
		fmt.Printf("Running %v.%v\n", task.getType(), task.getName())

		if err := context.pushWorkingDirectory(task); err != nil {
			return errors.Wrap(err, 0)
		}

		input, err := buildTaskInput(task, context)

		if err != nil {
			return errors.Wrap(err, 0)
		}

		output, err := task.run(context, input)

		if err != nil {
			return errors.Wrap(err, 0)
		}

		if output == nil {
			return errors.Errorf("Output is null for task %v", task)
		}

		context.outputs[task.getPath(task.getType())] = output

		if err := context.popWorkingDirectory(); err != nil {
			return errors.Wrap(err, 0)
		}
	}

	fmt.Printf("Plan done\n")
	return nil
}

func buildTaskInput(task Task, context *Context) (*TaskInput, error) {
	vars := make(map[string]string)

	for _, exprstr := range task.getExpressions() {
		expr := Expression{
			Expr: exprstr,
		}

		for _, token := range expr.getTokens() {
			dotIndex := strings.LastIndex(token, ".")

			if dotIndex == -1 {
				return nil, errors.Errorf("Invalid reference %v", token)
			}

			path := token[0:dotIndex]
			name := token[dotIndex+1:]

			outputTask, err := task.TaskByUri(path)

			if err != nil {
				return nil, errors.Wrap(err, 0)
			}

			output, ok := context.outputs[outputTask.getPath(outputTask.getType())]

			if !ok {
				return nil, errors.Errorf("Missing task output %v from %v", outputTask.getPath(outputTask.getType()), task.getPath(task.getType()))
			}

			val, ok := output.vars[name]

			if !ok {
				return nil, errors.Errorf("Missing input %v from %v", name, outputTask.getPath(outputTask.getType()))
			}

			vars[token] = val
		}
	}

	return &TaskInput{
		vars: vars,
	}, nil
}

type Expression struct {
	Expr string
}

func (e *Expression) getTokens() []string {
	r := regexp.MustCompile(`\${[^}]*}`)
	matches := r.FindAllString(e.Expr, -1)

	for idx, match := range matches {
		match = match[2 : len(match)-1]
		matches[idx] = match
	}

	return matches
}

func (e *Expression) Evaluate(input *TaskInput) (string, error) {
	tmplstring := e.Expr
	values := make(map[string]string)

	isBashSnippet := strings.HasPrefix(tmplstring, "{{bash")
	isValidGoTemplate := strings.HasPrefix(tmplstring, "{{")

	for _, token := range e.getTokens() {
		val, ok := input.vars[token]

		if !ok {
			return "", errors.Errorf("Missing token %v", token)
		}

		escapedToken := strings.ReplaceAll(token, ".", "_")
		escapedToken = strings.ReplaceAll(escapedToken, "-", "_")

		if isBashSnippet {
			tmplstring = strings.ReplaceAll(tmplstring, "${"+token+"}", val)
		} else {
			if isValidGoTemplate {
				tmplstring = strings.ReplaceAll(tmplstring, "${"+token+"}", "."+escapedToken)
			} else {
				tmplstring = strings.ReplaceAll(tmplstring, "${"+token+"}", "{{."+escapedToken+"}}")
			}

			values[escapedToken] = val
		}
	}

	if isBashSnippet {
		return e.runBashSnippet(tmplstring[len("{{bash") : len(tmplstring)-3])
	}

	funcMap := template.FuncMap{
		"bash": func(script string) (string, error) {
			fmt.Printf("%v\n", script)
			file, err := ioutil.TempFile(os.TempDir(), "stitcher-bash-*.sh")

			if err != nil {
				return "", errors.Wrap(err, 0)
			}

			defer os.Remove(file.Name())
			defer file.Close()

			file.Write([]byte("set -o pipefail\n" + script))

			exec := Executable{
				Args: []string{
					file.Name(),
				},
			}

			result, err := exec.Run("/bin/bash", false)

			if err != nil {
				return "", errors.Wrap(err, 0)
			}

			if len(result.Stdout) > 0 && result.Stdout[len(result.Stdout)-1] == '\n' {
				return result.Stdout[0 : len(result.Stdout)-1], nil
			}

			return result.Stdout, nil
		},
		"readFile": func(path string) (string, error) {
			bytes, err := ioutil.ReadFile(path)

			if err != nil {
				return "", errors.Wrap(err, 0)
			}

			return string(bytes), nil
		},
		"base64": func(val string) (string, error) {
			return base64.StdEncoding.EncodeToString([]byte(val)), nil
		},
		"absolute_path": func(path string) (string, error) {
			return filepath.Abs(path)
		},
	}

	tmpl, err := template.New("expr").Funcs(funcMap).Parse(tmplstring)

	if err != nil {
		fmt.Printf("Failed template %v\n", tmplstring)
		return "", errors.Wrap(err, 0)
	}

	var output strings.Builder

	err = tmpl.Execute(&output, values)

	if err != nil {
		return "", errors.Wrap(err, 0)
	}

	return output.String(), nil
}

func (e *Expression) runBashSnippet(script string) (string, error) {
	file, err := ioutil.TempFile(os.TempDir(), "stitcher-bash-*.sh")

	if err != nil {
		return "", errors.Wrap(err, 0)
	}

	defer os.Remove(file.Name())
	defer file.Close()

	file.Write([]byte("set -o pipefail\n" + script))

	exec := Executable{
		Args: []string{
			file.Name(),
		},
	}

	result, err := exec.Run("/bin/bash", false)

	if err != nil {
		fmt.Printf("Failed script %v\n", script)
		return "", errors.Wrap(err, 0)
	}

	if len(result.Stdout) > 0 && result.Stdout[len(result.Stdout)-1] == '\n' {
		return result.Stdout[0 : len(result.Stdout)-1], nil
	}

	return result.Stdout, nil
}

type Directory struct {
	name             string
	parent           *Directory
	children         map[string]*Directory
	tasks            map[string]Task
	shortNamesIndex  map[string][]Task
	workingDirectory string
}

func NewDirectory(name string, parent *Directory) *Directory {
	return &Directory{
		name:            name,
		parent:          parent,
		children:        make(map[string]*Directory),
		tasks:           make(map[string]Task),
		shortNamesIndex: make(map[string][]Task),
	}
}

func (d *Directory) addTask(task Task) error {
	if err := task.setParent(d); err != nil {
		return errors.Wrap(err, 0)
	}

	taskId := task.getId(task.getType())
	d.tasks[taskId] = task
	d.getRoot().shortNamesIndex[taskId] = append(d.shortNamesIndex[taskId], task)
	return nil
}

func (d *Directory) getRoot() *Directory {
	if d.parent == nil {
		return d
	}

	return d.parent.getRoot()
}

func (d *Directory) TaskByUri(uri string) (Task, error) {
	if d.parent != nil {
		return d.getRoot().TaskByUri(uri)
	}

	parts := strings.Split(uri, ".")

	if len(parts) < 1 {
		return nil, errors.Errorf("Invalid task uri", uri)
	}

	if len(parts) < 3 {
		tasks, ok := d.shortNamesIndex[uri]

		if ok {
			if len(tasks) > 1 {
				return nil, errors.Errorf("Found more than a single task with name %v", uri)
			}

			return tasks[0], nil
		}
	}

	return d.findTask(parts)
}

func (d *Directory) findTask(uriParts []string) (Task, error) {
	if len(uriParts) < 2 {
		taskName := strings.Join(uriParts[0:], ".")
		task, ok := d.tasks[taskName]

		if !ok {
			return nil, errors.Errorf("Task not found by full name %v", taskName)
		}

		return task, nil
	}

	dir, ok := d.children[uriParts[0]]

	if !ok {
		return nil, errors.Errorf("Directory not found %v in %v", uriParts, d.GetPath())
	}

	return dir.findTask(uriParts[1:])
}

func (d *Directory) GetPath() string {
	if d.parent == nil {
		return d.name
	}

	path := d.parent.GetPath()

	if path == "" {
		return d.name
	} else {
		return d.parent.GetPath() + "." + d.name
	}
}

func (d *Directory) getWorkingDirectory() string {
	if d.workingDirectory != "" && strings.HasPrefix(d.workingDirectory, "/") {
		return d.workingDirectory
	}

	var dir string

	if d.parent == nil {
		dir = d.name
	} else {
		dir = d.parent.getWorkingDirectory()
	}

	if d.workingDirectory != "" {
		return filepath.Join(dir, d.workingDirectory)
	} else {
		return filepath.Join(dir, d.name)
	}
}

func (d *Directory) DecodeYaml(y map[interface{}]interface{}) error {
	for keyobj, value := range y {
		key, ok := keyobj.(string)

		if !ok {
			return errors.Errorf("Key is not a string - %v", key)
		}

		if strings.HasPrefix(key, "./") {
			dirname := key[2:]
			newdir := NewDirectory(dirname, d)
			d.children[dirname] = newdir

			valuemap, ok := (value).(map[interface{}]interface{})

			if !ok {
				return errors.Errorf("Value is not a map - %t", value)
			}

			if err := newdir.DecodeYaml(valuemap); err != nil {
				return errors.Wrap(err, 0)
			}
		} else {
			parts := strings.Split(key, ".")

			if len(parts) < 1 || len(parts) > 2 {
				return errors.Errorf("Invalid task name %v", key)
			}

			tasktype := parts[0]
			name := ""

			if len(parts) > 1 {
				name = parts[1]
			}

			task, err := registry.build(tasktype, name)

			if err != nil {
				return errors.Wrap(err, 0)
			}

			task.setName(name)

			marshalledtask, err := yaml.Marshal(value)

			if err != nil {
				return errors.Wrap(err, 0)
			}

			decoder := yaml.NewDecoder(bytes.NewReader(marshalledtask))
			decoder.Decode(task)

			d.addTask(task)
		}
	}

	return nil
}

func (d *Directory) PrintTree(depth int) {
	prefix := strings.Repeat("\t", depth)

	if depth == 0 {
		fmt.Printf("Root\n")
	} else {
		fmt.Printf("%v%v\n", prefix, d.name)
	}

	for _, dir := range d.children {
		dir.PrintTree(depth + 1)
	}

	for _, task := range d.tasks {
		if task.getName() == "" {
			fmt.Printf("%v\t%v (%v)\n", prefix, task.getType(), task.getWorkingDirectory())
		} else {
			fmt.Printf("%v\t%v.%v (%v)\n", prefix, task.getType(), task.getName(), task.getWorkingDirectory())
		}
		fmt.Printf("%v\t %+v\n\n", prefix, task.getExpressions())
	}
}

var registry TaskRegistry

type TaskRegistry struct {
	types map[string]reflect.Type
}

func (r *TaskRegistry) build(tasktype string, name string) (Task, error) {
	t, ok := r.types[tasktype]

	if !ok {
		return nil, errors.Errorf("Unknown task type %v", tasktype)
	}

	newtaskobj := reflect.New(t).Interface()
	newtask, ok := newtaskobj.(Task)

	if !ok {
		return nil, errors.Errorf("Invalid task type %t", newtaskobj)
	}

	return newtask, nil
}

func mainRun() error {
	registry = TaskRegistry{
		types: map[string]reflect.Type{
			"script":           reflect.TypeOf(ScriptTask{}),
			"envs":             reflect.TypeOf(EnvsTask{}),
			"vars":             reflect.TypeOf(VarsTask{}),
			"properties":       reflect.TypeOf(PropertiesTask{}),
			"terraform":        reflect.TypeOf(TerraformTask{}),
			"ansible-playbook": reflect.TypeOf(AnsiblePlaybookTask{}),
			"kubernetes":       reflect.TypeOf(KubernetesTask{}),
			"helmfile":         reflect.TypeOf(HelmfileTask{}),
		},
	}

	root := NewDirectory("", nil)

	bytes, err := ioutil.ReadFile("/home/david/work/my-projects/stitcher/stitcher.yml")

	if err != nil {
		return errors.Wrap(err, 0)
	}

	y := make(map[interface{}]interface{})
	yaml.Unmarshal(bytes, y)

	if err := root.DecodeYaml(y); err != nil {
		return errors.Wrap(err, 0)
	}

	envs := EnvsTask{
		CommonTask: CommonTask{
			name: "",
		},
	}

	root.addTask(&envs)

	root.PrintTree(0)
	fmt.Printf("---------------\n")

	taskToRun := []string{"ami.script"}

	plan, err := BuildPlan(root, taskToRun)

	if err != nil {
		return errors.Wrap(err, 0)
	}

	fmt.Printf("Plan is %+v\n", plan)

	cwd, err := os.Getwd()

	if err != nil {
		return errors.Wrap(err, 0)
	}

	context := Context{
		outputs:               make(map[string]*TaskOutput),
		workingDirectoryStack: []string{cwd, "/home/david/work/sightd/git/sgh-ops"},
	}

	err = plan.run(&context)

	if err != nil {
		return errors.Wrap(err, 0)
	}

	return nil
}

func main() {
	err := mainRun()

	if err != nil {
		switch err := err.(type) {
		case *errors.Error:
			fmt.Printf("Error: %v\n", err.ErrorStack())
		default:
			fmt.Printf("Error: %v\n", err)
		}

		os.Exit(1)
	}
}
