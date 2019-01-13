package jobs

// TODO: Decide whether to rely on "k8s.io/api/core/v1"
// or "golang.org/x/build/kubernetes/api"
// Both claim v1 compliance, client-go uses the latter

// TODO: Add caching layer

import (
	"errors"
	"fmt"
	"io/ioutil"
	"log"

	jsoniter "github.com/json-iterator/go"
	kubeApi "golang.org/x/build/kubernetes/api"

	"github.com/google/uuid"

	"database/sql"
	_ "github.com/go-sql-driver/mysql" // must be blank; satisfies sql interface

	yaml "gopkg.in/yaml.v2"
)

var jsonFast = jsoniter.ConfigFastest
var instanceID = uuid.New().String()

const (
	Cancelled   int16 = -3
	Initialized int16 = -2
	Created     int16 = -1
)

type dbConfig struct {
	DB struct {
		// https://github.com/go-sql-driver/mysql#dsn-data-source-name
		DSN string
	}
}

// Jobs defines shared resources needed to manage pods
// contains a database handle and a map of prepared, commonly-used SQL statements
// These statements need to be closed after Jobs is no longer used, to prevent memory leaks
type Jobs struct {
	db         *sql.DB
	statements map[string]*sql.Stmt
}

// New instantiates the Jobs struct, storing a handle to the relevant database
// and prepares/stores commonly used sql statements to avoid perf overhead
func New() (*Jobs, error) {
	var dbConf dbConfig

	// TODO: Lock and read/unmarshal this only once
	d, err := ioutil.ReadFile("./jobs.yml")

	if err != nil {
		return nil, err
	}

	err = yaml.Unmarshal(d, &dbConf)

	if err != nil {
		return nil, err
	}

	// No need to close/defer close
	// https://golang.org/pkg/database/sql/#Open
	db, err := sql.Open("mysql", dbConf.DB.DSN)

	if err != nil {
		return nil, err
	}

	statements, err := prepareStatements(db)

	if err != nil {
		return nil, err
	}

	j := &Jobs{
		db:         db,
		statements: statements,
	}

	return j, nil
}

// Cleanup closes all prepared statements to avoid leaking memory
func Cleanup(j *Jobs) {
	for k, v := range j.statements {
		log.Printf("Closing prepared statement %s", k)
		v.Close()
	}

	j.db.Close()
}

func prepareStatements(db *sql.DB) (map[string]*sql.Stmt, error) {
	s := make(map[string]*sql.Stmt, 3)
	var err error

	s["jobs.create"], err = db.Prepare("INSERT INTO jobs.job (kube_id,attributes,callback,pod_template) VALUES(?,?,?,?,?)")
	s["jobs.update.container_state.kube_id"], err = db.Prepare("UPDATE jobs.job SET container_state=?,status=? WHERE kube_id=?")
	s["batch.create"], err = db.Prepare("INSERT INTO jobs.batch (kube_id,attributes,callback,pod_template) VALUES(?,?,?,?,?)")
	s["job_batch.create"], err = db.Prepare("INSERT INTO jobs.job_batch (job_id,batch_id) VALUES(?,?)")

	return s, err
}

// JobRequest defines the structure of the JSON string that is sent to batch server
// to generate a new batch job
type JobRequest struct {
	// *int to allow us to distinguish missingness and 0
	BatchID *int `json:"batch_id,omitempty"`
	// TODO: Explain use cases
	Attributes map[string]string `json:"attributes"`
	// cd ..
	Callback string          `json:"callback"`
	Spec     kubeApi.PodSpec `json:"spec"`
}

// The resulting job
type Job struct {
	ID int `json:"id"`
	// TODO: Decide if we need this, or whether to make database id the
	// kubernetes-side id. Reasons not to use ID: either need uuid (4 bytes) or 2 sql trips
	KubeID      string                   `json:"kube_id"`
	BatchID     int                      `json:"batch_id"`
	Attributes  map[string]string        `json:"attributes"`
	Callback    string                   `json:"callback"`
	PodTemplate *kubeApi.PodTemplateSpec `json:"pod_template"`
	PodStatus   *kubeApi.PodStatus       `json:"pod_status"`
	// ExitCode: Values less than 0 indicate initialization or cancelleation
	// while values 0 or greater indicate some completion s Change from Batch 0.2 behavior
	// No longer dedicated "State" variable
	// ExitCo
	ExitCode int16 `json:"exit_code"`
}

// Create a common ID, Attributes map[string] for a number of jobs
// We handle this by creating a new entyr in the Batches MySQL table
// And
type Batch struct {
	ID         int               `json:"id"`
	KubeID     string            `json:"kube_id"`
	Attributes map[string]string `json:"attributes"`
}

// Takes a json string, validates it to match Kubernetes V1 Api
// TODO: Consider BatchID check: may be useful to use a validation library
func CreateJob(jobs *Jobs, jsonStr []byte) (*Job, error) {
	req, err := unmarshal(jsonStr)

	if err != nil {
		return nil, err
	}

	if req.BatchID == nil {
		return nil, errors.New("Must provide batch_id")
	}

	// Avoid 2nd mysql insert
	kubeID := uuid.New().String()

	podTemplate := &kubeApi.PodTemplateSpec{
		ObjectMeta: kubeApi.ObjectMeta{
			// Is the extra dash to allow an id for non-job jobs?
			GenerateName: fmt.Sprintf("job-%s-", kubeID),
			Labels: map[string]string{
				"app":                    "batch-job",
				"hail.is/batch-instance": instanceID,
				"uuid":                   kubeID,
			},
		},
		Spec: req.Spec,
	}

	pJSON, err := jsonFast.Marshal(podTemplate)

	if err != nil {
		return nil, err
	}

	// 0 exit_status is the default; only makes sense in context of status == Complete / Cancelled
	result, err := jobs.statements["jobs.create"].Exec(
		kubeID, req.Attributes, req.Callback, pJSON, 0,
	)

	id, err := result.LastInsertId()

	if err != nil {
		return nil, err
	}

	// TODO: potentially combine into one compound query
	result, err = jobs.statements["job_batch.create"].Exec(
		id, *req.BatchID,
	)

	if err != nil {
		return nil, err
	}

	j := &Job{
		ID:          int(id),
		BatchID:     *req.BatchID,
		Attributes:  req.Attributes,
		PodTemplate: podTemplate,
		Callback:    req.Callback,
		// Job is created when Kubernetes successfully creates this job
		// TODO: Should we wait until
		ExitCode: Initialized,
	}

	return j, nil
}

func MarkJob(job *Job, id int, status int16) error {
	if status < Cancelled {
		return errors.New("Invalid status, expect -3 to 255")
	}

	return nil
}

// This is a separate function to allow swapping of unmarshal functions
func unmarshal(jsonStr []byte) (*JobRequest, error) {
	var j *JobRequest

	err := jsonFast.Unmarshal(jsonStr, &j)

	return j, err
}

// CreateBatch inserts a new entry in the jobs.batch table, and returns a JSON
// object with the inserted id, as well as the passed-in attributes
// func CreateBatch(attributes []byte) (Batch, err) {

// }
