package main

import (
	"github.com/go-errors/errors"
	"bytes"
	"bufio"
	"reflect"
	"encoding/base64"
	"fmt"
	execute "github.com/alexellis/go-execute/pkg/v1"
	"io/ioutil"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"text/template"
	"gopkg.in/yaml.v2"
	"github.com/stevenle/topsort"
)

type Context struct {
	outputs map[string]*TaskOutput
}

type TaskInput struct {
	vars             map[string]string
	WorkingDirectory string
}

type TaskOutput struct {
	vars map[string]string
}

type Task interface {
	getWorkingDirecotry() string
	getType() string
	getName() string
	setName(name string)
	setParent(parent *Directory) error
	getPath(tasktype string) string
	getId(tasktype string) string
	run(context *Context, input *TaskInput) (*TaskOutput, error)
	getExpressions() []string
	TaskByUri(uri string) (Task, error)
}

type CommonTask struct {
	parent           *Directory
	name             string
	WorkingDirectory string
}

func (t *CommonTask) setParent(parent *Directory) error {
	if t.parent != nil {
		return errors.Errorf("Task already belong to another parent %v", t)
	}

	t.parent = parent
	return nil
}

func (t *CommonTask) setName(name string) {
	t.name = name
}

func (t *CommonTask) getName() string {
	return t.name
}

func (t *CommonTask) getWorkingDirecotry() string {
	if t.WorkingDirectory != "" {
		return t.WorkingDirectory
	}

	return filepath.Join(t.parent.WorkingDirectory, t.name)
}

func (t *CommonTask) getPath(tasktype string) string {
	path := t.parent.GetPath()
	
	if path == "" {
		return t.getId(tasktype)
	} else {
		return path + "." + t.getId(tasktype)
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
	Args []string `yaml:"args"`
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

	return e.internalRun(exec, input.WorkingDirectory, envs, args, true)
}

func (e *Executable) Run(exec string, dir string, streamStdio bool) (*execute.ExecResult, error) {
	envs := make([]string, 0)

	for key, val := range e.Envs {
		envs = append(envs, key+"="+val)
	}

	return e.internalRun(exec, dir, envs, e.Args, streamStdio)
}

func (e *Executable) internalRun(exec string, dir string, envs []string, args []string, streamStdio bool) (*execute.ExecResult, error) {
	cmd := execute.ExecTask{
		Command:      exec,
		Cwd:          dir,
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
	CommonTask
	Executable `yaml:",inline"`
	File string
}

func (t *ScriptTask) getExpressions() []string {
	return append(t.Executable.getExpressions(), t.File)
}

func (t *ScriptTask) getType() string {
	return "script"
}

func (t *ScriptTask) run(context *Context, input *TaskInput) (*TaskOutput, error) {
	fmt.Printf("Script task running\n")

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
	CommonTask
	Files []string `yaml:"files"`
}

func (t *PropertiesTask) getExpressions() []string {
	return t.Files
}

func (t *PropertiesTask) getType() string {
	return "properties"
}

func (t *PropertiesTask) run(context *Context, input *TaskInput) (*TaskOutput, error) {
	fmt.Printf("Properties task running\n")

	vars := make(map[string]string)

	for _, path := range t.Files {
		file, err := os.Open(path)

		if err != nil {
			return nil, errors.Wrap(err, 0)
		}

		defer file.Close()

		scanner := bufio.NewScanner(file)

		for scanner.Scan() {
			line := scanner.Text()

			if strings.HasPrefix(strings.TrimSpace(line), "#") {
				continue
			}

			equalIndex := strings.Index(line, "=")

			if equalIndex == -1 {
				return nil, errors.Errorf("Invalid line %v in properties file %v", line, path)
			}

			key := line[0:equalIndex]
			value := line[equalIndex+1 : len(line)]

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
	CommonTask
	Vars map[string]string `yaml:",inline"`
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
		vars:vars,
	}, nil
}

type EnvsTask struct {
	CommonTask
	Envs map[string]string `yaml:",inline"`
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
		vars:vars,
	}, nil
}

type TerraformInit struct {
	Executable `yaml:",inline"`
}

type TerraformTask struct {
	CommonTask
	Location string `yaml:"location"`
	Executable `yaml:",inline"`
	Init TerraformInit `yaml:"init"`
}

func (t *TerraformTask) getExpressions() []string {
	result := append(t.Executable.getExpressions(), t.Init.getExpressions()...)
	return append(result, t.Location)
}

func (e *TerraformTask) getType() string {
	return "terraform"
}

func (e *TerraformTask) run(context *Context, input *TaskInput) (*TaskOutput, error) {
	return nil, errors.New("Not implemented")
}

type AnsiblePlaybookTask struct {
	CommonTask
	Executable `yaml:",inline"`
	Playbook string `yaml:"playbook"`
	Inventory string `yaml:"inventory"`
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
	CommonTask
	Executable `yaml:",inline"`
	File string `yaml:"file"`
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
	CommonTask
	Executable `yaml:",inline"`
	File string `yaml:"file"`
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
	
	for  dependent, dependentTask := range tasks {
		for dependee, _ := range dependentTask.Dependencies {
			if err := graph.AddEdge(dependent, dependee); err != nil {
				return nil, errors.Wrap(err, 0)
			}
		}
	}
	
	alreadyAdded := make(map[string]bool)
	finalTasks := make([]Task,0)
	
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
			
			if (!ok) {
				return nil, errors.Errorf("Task not found in sorted list %v", sortedTaskName)
			}
			
			added, ok := alreadyAdded[sortedTaskName]
			
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
	
	if (err != nil) {
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
	Task Task
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
		Task:task,
		Dependencies:dependencies,
	}, nil
}

func (p *Plan) run(context *Context) error {
	fmt.Printf("Plan started\n")

	for _, task := range p.tasks {
		input, err := buildTaskInput(task, context)

		if err != nil {
			return errors.Wrap(err, 0)
		}

		output, err := task.run(context, input)

		if err != nil {
			return errors.Wrap(err, 0)
		}

		context.outputs[task.getPath(task.getType())] = output
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
			name := token[dotIndex+1 : len(token)]

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
		vars:             vars,
		WorkingDirectory: "/tmp",
	}, nil
}

type Expression struct {
	Expr string
}

func (e *Expression) getTokens() []string {
	r := regexp.MustCompile("\\${[^}]*}")
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
	isValidGoTemplate := strings.HasPrefix(tmplstring, "{{")

	for _, token := range e.getTokens() {
		val, ok := input.vars[token]

		if !ok {
			return "", errors.Errorf("Missing token %v", token)
		}

		escapedToken := strings.ReplaceAll(token, ".", "_")

		if isValidGoTemplate {
			tmplstring = strings.ReplaceAll(tmplstring, "${"+token+"}", "."+escapedToken)
		} else {
			tmplstring = strings.ReplaceAll(tmplstring, "${"+token+"}", "{{."+escapedToken+"}}")
		}

		values[escapedToken] = val
	}

	funcMap := template.FuncMap{
		"bash": func(script string) (string, error) {
			file, err := ioutil.TempFile(os.TempDir(), "stitcher-bash-******.sh")

			if err != nil {
				return "", err
			}

			defer os.Remove(file.Name())
			defer file.Close()

			file.Write([]byte(script))

			exec := Executable{
				Args: []string{
					file.Name(),
				},
			}

			result, err := exec.Run("/bin/bash", input.WorkingDirectory, false)

			if err != nil {
				return "", err
			}

			if result.Stdout[len(result.Stdout)-1] == '\n' {
				return result.Stdout[0 : len(result.Stdout)-1], nil
			}

			return result.Stdout, nil
		},
		"readFile": func(path string) (string, error) {
			bytes, err := ioutil.ReadFile(path)

			if err != nil {
				return "", err
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
		return "", errors.Errorf("Error parsing template %v - %v", tmplstring, err)
	}

	var output strings.Builder

	err = tmpl.Execute(&output, values)

	if err != nil {
		return "", errors.Errorf("Error executing template %v - %v", tmplstring, err)
	}

	return output.String(), nil
}

type Directory struct {
	name             string
	parent           *Directory
	children         map[string]*Directory
	tasks            map[string]Task
	WorkingDirectory string
	shortNamesIndex  map[string][]Task
}

func NewDirectory(name string, parent *Directory) *Directory {
	return &Directory{
		name:             name,
		parent:           parent,
		children:         make(map[string]*Directory, 0),
		tasks:            make(map[string]Task, 0),
		shortNamesIndex:  make(map[string][]Task, 0),
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
		taskName := strings.Join(uriParts[0:len(uriParts)], ".")
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

	return dir.findTask(uriParts[1:len(uriParts)])
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

func (d *Directory) DecodeYaml(y map[interface{}]interface{}) error {
	for keyobj, value := range y {
		key, ok := keyobj.(string)
		
		if !ok {
			return errors.Errorf("Key is not a string - %v", key)
		}
		
		if strings.HasPrefix(key, "./") {
			dirname := key[2:len(key)]
			newdir := NewDirectory(dirname, d)
			d.children[dirname] = newdir
			
			valuemap, ok := (value).(map[interface{}]interface{});
			
			if !ok {
				return errors.Errorf("Value is not a map - %t", value)
			}
			
			if err := newdir.DecodeYaml(valuemap); err != nil {
				return errors.Wrap(err, 0)
			}
		} else {
			parts := strings.Split(key, ".")
			
			if (len(parts) < 1 || len(parts) > 2) {
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
			fmt.Printf("%v\t%v\n", prefix, task.getType())
		} else {
			fmt.Printf("%v\t%v.%v\n", prefix, task.getType(), task.getName())
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
		types:map[string]reflect.Type{
			"script":reflect.TypeOf(ScriptTask{}),
			"envs":reflect.TypeOf(EnvsTask{}),
			"vars":reflect.TypeOf(VarsTask{}),
			"properties":reflect.TypeOf(PropertiesTask{}),
			"terraform":reflect.TypeOf(TerraformTask{}),
			"ansible-playbook":reflect.TypeOf(AnsiblePlaybookTask{}),
			"kubernetes":reflect.TypeOf(KubernetesTask{}),
			"helmfile":reflect.TypeOf(HelmfileTask{}),
		},
	}
	
	root := NewDirectory("", nil)
	
	bytes, err := ioutil.ReadFile("/home/david/work/my-projects/stitcher/stitcher.yml")

	if err != nil {
		return err
	}

	y := make(map[interface{}]interface{})
	yaml.Unmarshal(bytes, y)

	if err := root.DecodeYaml(y); err != nil {
		return err
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
		return err
	}
	
	context := Context{
		outputs: make(map[string]*TaskOutput),
	}

	err = plan.run(&context)

	if err != nil {
		return err
	}
	
	return nil
	
	
	// cwd, err := os.Getwd()

	// if err != nil {
	// 	fmt.Printf("Error %v\n", err)
	// 	return
	// }

	// envs := EnvsTask{
	// 	CommonTask: CommonTask{
	// 		name: "",
	// 	},
	// 	Envs: map[string]string{
	// 		"SOME_ENVIRONMENT": "Some value",
	// 		"SOME_COMPLEX_ENVIRONMENT": `{{ "." | absolute_path }}`,
	// 	},
	// }

	// props := PropertiesTask{
	// 	CommonTask: CommonTask{
	// 		name: "common",
	// 	},
	// 	Files: []string{
	// 		"/home/david/temp/temp/2021-09-30-230206/test.properties",
	// 	},
	// }

	// script := ScriptTask{
	// 	CommonTask: CommonTask{
	// 		name: "test",
	// 	},
	// 	File: "/home/david/temp/temp/2021-09-30-230206/test.sh",
	// 	Executable: Executable{
	// 		Envs: map[string]string{
	// 			"Dude":  `${properties.common.Dude} ${envs.SOME_ENVIRONMENT}`,
	// 			"HELLO": `{{ ${properties.common.HELLO} | readFile | base64 }}`,
	// 		},
	// 		Args: []string{
	// 			`{{ "echo bash-func" | bash }}`,
	// 			"arg2",
	// 			"${properties.common.Arg}",
	// 			"${envs.SOME_COMPLEX_ENVIRONMENT}",
	// 			"${envs.SIGHTD_SECRETS_DIRECTORY}",
	// 		},
	// 	},
	// }

	// root.addTask(&envs)
	// root.addTask(&props)
	// root.addTask(&script)

	// plan := Plan{
	// 	tasks: []Task{
	// 		&envs,
	// 		&props,
	// 		&script,
	// 	},
	// }

	// context := Context{
	// 	outputs: make(map[string]*TaskOutput),
	// }

	// err = plan.run(&context)

	// if err != nil {
	// 	fmt.Printf("Error %v\n", err)
	// }
}

func main() {
	err := mainRun()
	
	if err != nil {
		switch err := err.(type) {
		case *errors.Error:
			fmt.Println("Error:", err.ErrorStack())
		default:
			fmt.Printf("Error:", err)
		}

		os.Exit(1)
	}
}
