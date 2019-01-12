package jobs

// TODO: Decide whether to rely on "k8s.io/api/core/v1"
// or "golang.org/x/build/kubernetes/api"
// Both claim v1 compliance, client-go uses the latter

// TODO: Add caching layer

import (
	"io/ioutil"
	"log"

	"github.com/akotlar/hail-go-batch/utils"

	jsoniter "github.com/json-iterator/go"
	kubeApi "golang.org/x/build/kubernetes/api"

	"github.com/google/uuid"

	"database/sql"
	_ "github.com/go-sql-driver/mysql" // must be blank; satisfies sql interface

	yaml "gopkg.in/yaml.v2"
)

var jsonFast = jsoniter.ConfigFastest
var instanceID = uuid.New().String()

type dbConfig struct {
	DB struct {
		// https://github.com/go-sql-driver/mysql#dsn-data-source-name
		DSN string
	}
}

// Jobs contains a database handle and a map of prepared, commonly-used SQL statements
type Jobs struct {
	db         *sql.DB
	statements map[string]*sql.Stmt
}

// New instantiates the Jobs struct, storing a handle to the relevant database
// and prepares/stores commonly used sql statements to avoid perf overhead
// These statements must be .Close() to avoid memory leak, using Jobs.Cleanup()
func New() *Jobs {
	var dbConf dbConfig

	d, err := ioutil.ReadFile("./jobs.yml")

	utils.PanicErr(err)

	yaml.Unmarshal(d, &dbConf)

	// No need to close/defer close
	// https://golang.org/pkg/database/sql/#Open
	db, err := sql.Open("mysql", dbConf.DB.DSN)

	utils.PanicErr(err)

	statements := prepareStatements(db)

	j := &Jobs{
		db:         db,
		statements: statements,
	}

	return j
}

// Cleanup closes all prepared statements to avoid leaking memory
func Cleanup(j *Jobs) {
	for k, v := range j.statements {
		log.Printf("Closing prepared statement %s", k)
		v.Close()
	}

	j.db.Close()
}

func prepareStatements(db *sql.DB) map[string]*sql.Stmt {
	s := make(map[string]*sql.Stmt, 3)

	s["jobs.create"] = prepareStmt(db, "INSERT INTO jobs.job (attributes,callback,pod_template,exit_code) VALUES(?,?,?,?,?)")
	s["batch.create"] = prepareStmt(db, "INSERT INTO jobs.batch (attributes,callback,pod_template,exit_code) VALUES(?,?,?,?,?)")
	s["job_batch.create"] = prepareStmt(db, "INSERT INTO jobs.job_batch (job_id,batch_id) VALUES(?,?)")

	return s
}

func prepareStmt(db *sql.DB, query string) *sql.Stmt {
	stmt, err := db.Prepare(query)

	utils.PanicErr(err)

	return stmt
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
	ID          int                      `json:"id"`
	BatchID     int                      `json:"batch_id"`
	Attributes  map[string]string        `json:"attributes"`
	Callback    string                   `json:"callback"`
	PodTemplate *kubeApi.PodTemplateSpec `json:"pod_template"`
	// Currently not implemented
	// ContainerState kubeApi.ContainerState `json:"container_state"`
	// One of Created/Complete/Cancelled
	State    string `json:"state"`
	ExitCode int    `json:"exit_code"`
}

// Create a common ID, Attributes map[string] for a number of jobs
// We handle this by creating a new entyr in the Batches MySQL table
// And
type Batch struct {
	Attributes map[string]string `json:"attributes"`
}

const (
	STATUS_INIT      = "Initialized"
	STATUS_CREATED   = "Created"
	STATUS_COMPLETED = "Completed"
	STATUC_CANCELLED = "Cancelled"
)

// Takes a json string, validates it to match Kubernetes V1 Api
// TODO: Consider BatchID check: may be useful to use a validation library
func CreateJob(jobs *Jobs, jsonStr []byte) *Job {
	req := unmarshal(jsonStr)

	if req.BatchID == nil {
		panic("Must provide a batch_id")
	}

	podTemplate := &kubeApi.PodTemplateSpec{
		ObjectMeta: kubeApi.ObjectMeta{
			// Not storing generate-name, because we will be able to query
			// the stored JSON, don't maintain state to get a count-based "id"
			// don't want to incur an extra query, and using uuid to create a
			// unique name is either unnecessary, or non-informative (uuid label)
			Labels: map[string]string{
				"app":                    "batch-job",
				"hail.is/batch-instance": instanceID,
				"uuid":                   uuid.New().String(),
			},
		},
		Spec: req.Spec,
	}

	pJSON, err := jsonFast.Marshal(podTemplate)

	utils.PanicErr(err)

	result, err := jobs.statements["jobs.create"].Exec(
		req.Attributes, req.Callback, pJSON, -1,
	)

	id, err := result.LastInsertId()

	utils.PanicErr(err)

	result, err = jobs.statements["job_batch.create"].Exec(
		id, *req.BatchID,
	)

	utils.PanicErr(err)

	j := &Job{
		ID:          int(id),
		BatchID:     *req.BatchID,
		Attributes:  req.Attributes,
		PodTemplate: podTemplate,
		Callback:    req.Callback,
		State:       STATUS_INIT,
	}

	return j
}

func unmarshal(jsonStr []byte) *JobRequest {
	var j *JobRequest

	err := jsonFast.Unmarshal(jsonStr, &j)

	utils.PanicErr(err)

	return j
}

// CreateBatch inserts a new entry in the jobs.batch table, and returns a JSON
// object with the inserted id, as well as the passed-in attributes
// func CreateBatch(attributes []byte) (Batch, err) {

// }
