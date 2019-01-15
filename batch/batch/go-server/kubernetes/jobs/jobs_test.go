package jobs

import (
	"encoding/json"
	"fmt"
	"github.com/davecgh/go-spew/spew"
	"github.com/valyala/fastjson"
	"io/ioutil"
	"os"
	"testing"
)

const MYSQL_ROOT_PASSWORD = "root"
const MYSQL_USER = "batch-test"
const MYSQL_PASSWORD = "secure-password"
const MYSQL_DATABASE = "godb"

func TestCreate(t *testing.T) {
	json := setup("mock_job_json.json")

	job, err := CreateJob(json)

	if err != nil {
		t.Error(err)
	} else {
		t.Log("OKK")
	}

	spew.Dump(job)

}

func TestUnmarshal(t *testing.T) {
	json := setup("mock_job_json.json")

	job, err := unmarshal(json)

	spew.Dump(job)

	if err != nil {
		t.Error(err)
	} else {
		t.Log("OKK")
	}

	if job.BatchID != 1 {
		t.Error(fmt.Sprintf("Failed to parse batch_id. Got: %d", job.BatchID))
	}

	if job.Attributes == nil || !(job.Attributes["a"] == "one" && job.Attributes["b"] == "two") {
		t.Error(fmt.Sprintf("Failed to parse attributes. Got: %v", job.Attributes))
	}

	// if job.apiVersion != "v1" {
	// 	t.Error(fmt.Sprintf("Failed to parse apiVersion. Got: %s", job.apiVersion))
	// }

}

func TestNewIncomplete(t *testing.T) {
	json := setup("mock_job_json.json")

	_, err := CreateJob(json)

	if err != nil {
		t.Error(err)
	} else {
		t.Log("OKK")
	}

	// if job == nil {

	// }

}

// https://github.com/alecthomas/go_serialization_benchmarks
// https://github.com/json-iterator/go-benchmark
// jsonIter performs about as well as all other methods
// besides fastjson, has a very simple runtime mode, and can be made faster
// through use of codegen, or afterburner
func BenchmarkNewStd(b *testing.B) {
	json := setup("mock_job_json.json")

	for i := 0; i < b.N; i++ {
		benchUnmarshalStd(json)
	}
}

func BenchmarkNewFast(b *testing.B) {
	json := setup("mock_job_json.json")

	for i := 0; i < b.N; i++ {
		bencUnmarshal(json)
	}
}

func BenchmarkNewFastNoStruct(b *testing.B) {
	json := setup("mock_job_json.json")

	for i := 0; i < b.N; i++ {
		benchUnmarshalFastNoStruct(json)
	}
}

func BenchmarkNewFastPersistNoStruct(b *testing.B) {
	json := setup("mock_job_json.json")

	for i := 0; i < b.N; i++ {
		benchUnmarshalFastPersistNoStruct(json)
	}
}

func bencUnmarshal(jsonStr []byte) error {
	var j JobRequest

	err := jsonFast.Unmarshal(jsonStr, &j)

	if err != nil {
		panic(err)
	}

	// fmt.Print(j)
	return err
}

func benchUnmarshalStd(jsonStr []byte) error {
	var j JobRequest

	err := json.Unmarshal(jsonStr, &j)

	if err != nil {
		panic(err)
	}

	// fmt.Print(j)
	return err
}

// 10x faster than jsoniter, even with the assignment of the resulting job object
// Only 2
var p fastjson.Parser

func benchUnmarshalFastNoStruct(jsonStr []byte) error {
	j, err := p.ParseBytes(jsonStr)

	if err != nil || j == nil {
		panic(err)
	}
	// fmt.Print(j.spec)
	return err
}

// 2x faster when ensuring *Value is valid between goroutine calls
func benchUnmarshalFastPersistNoStruct(jsonStr []byte) error {
	var p fastjson.Parser
	j, err := p.ParseBytes(jsonStr)

	if err != nil || j == nil {
		panic(err)
	}
	// fmt.Print(j.spec)
	return err
}
func setup(filePath string) []byte {
	jsonFile, err := os.Open(filePath)
	// if we os.Open returns an error then handle it
	if err != nil {
		panic(err)
	}

	// defer the closing of our jsonFile so that we can parse it later on
	defer jsonFile.Close()

	// read our opened xmlFile as a byte array.
	json, _ := ioutil.ReadAll(jsonFile)

	return json
}

// // Thnx: https://github.com/MiteshSharma/DockerMysqlGo
// type mysqlDocker struct {
// 	Docker Docker
// }

// func (m *mysqlDocker) StartMysqlDocker() {
// 	mysqlOptions := map[string]string{
// 		"MYSQL_ROOT_PASSWORD": "root",
// 		"MYSQL_USER":          "go",
// 		"MYSQL_PASSWORD":      "root",
// 		"MYSQL_DATABASE":      "godb",
// 	}
// 	containerOption := ContainerOption{
// 		Name:              "batch-mysql-test",
// 		Options:           mysqlOptions,
// 		MountVolumePath:   "/var/lib/mysql",
// 		PortExpose:        "3306",
// 		ContainerFileName: "mysql:5.7",
// 	}
// 	m.Docker = Docker{}
// 	m.Docker.Start(containerOption)
// 	m.Docker.WaitForStartOrKill(mysqlStartTimeout)
// }

// func (m *mysqlDocker) Stop() {
// 	m.Docker.Stop()
// }

// func (d *Docker) isInstalled() bool {
// 	command := exec.Command("docker", "ps")
// 	err := command.Run()
// 	if err != nil {
// 		return false
// 	}
// 	return true
// }

// func (d *Docker) Start(c ContainerOption) (string, error) {
// 	dockerArgs := d.getDockerRunOptions(c)
// 	command := exec.Command("docker", dockerArgs...)
// 	command.Stderr = os.Stderr
// 	result, err := command.Output()
// 	if err != nil {
// 		return "", err
// 	}
// 	d.ContainerID = strings.TrimSpace(string(result))
// 	d.ContainerName = c.Name
// 	command = exec.Command("docker", "inspect", d.ContainerID)
// 	result, err = command.Output()
// 	if err != nil {
// 		d.Stop()
// 		return "", err
// 	}
// 	return string(result), nil
// }

// func (d *Docker) WaitForStartOrKill(timeout int) error {
// 	for tick := 0; tick < timeout; tick++ {
// 		containerStatus := d.getContainerStatus()
// 		if containerStatus == dockerStatusRunning {
// 			return nil
// 		}
// 		if containerStatus == dockerStatusExited {
// 			return nil
// 		}
// 		time.Sleep(time.Second)
// 	}
// 	d.Stop()
// 	return errors.New("Docker faile to start in given time period so stopped")
// }

// func (d *Docker) getContainerStatus() string {
// 	command := exec.Command("docker", "ps", "-a", "--format", "{{.ID}}|{{.Status}}|{{.Ports}}|{{.Names}}")
// 	output, err := command.CombinedOutput()
// 	if err != nil {
// 		d.Stop()
// 		return dockerStatusExited
// 	}
// 	outputString := string(output)
// 	outputString = strings.TrimSpace(outputString)
// 	dockerPsResponse := strings.Split(outputString, "\n")
// 	for _, response := range dockerPsResponse {
// 		containerStatusData := strings.Split(response, "|")
// 		containerStatus := containerStatusData[1]
// 		containerName := containerStatusData[3]
// 		if containerName == d.ContainerName {
// 			if strings.HasPrefix(containerStatus, "Up ") {
// 				return dockerStatusRunning
// 			}
// 		}
// 	}
// 	return dockerStatusStarting
// }

// func (d *Docker) getDockerRunOptions(c ContainerOption) []string {
// 	portExpose := fmt.Sprintf("%s:%s", c.PortExpose, c.PortExpose)
// 	var args []string
// 	for key, value := range c.Options {
// 		args = append(args, []string{"-e", fmt.Sprintf("%s=%s", key, value)}...)
// 	}
// 	args = append(args, []string{"--tmpfs", c.MountVolumePath, c.ContainerFileName}...)
// 	dockerArgs := append([]string{"run", "-d", "--name", c.Name, "-p", portExpose}, args...)
// 	return dockerArgs
// }

// func (d *Docker) Stop() {
// 	exec.Command("docker", "rm", "-f", d.ContainerID).Run()
// }
