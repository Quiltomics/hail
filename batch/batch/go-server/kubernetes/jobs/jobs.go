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
	coreV1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	// metaV1 "k8s.io/apimachinery/pkg/apis/core/v1"
	// fields "k8s.io/apimachinery/pkg/fields"
	// runtime "k8s.io/apimachinery/pkg/util/runtime"
	wait "k8s.io/apimachinery/pkg/util/wait"
	// kubernetes "k8s.io/client-go/kubernetes"
	cache "k8s.io/client-go/tools/cache"
	workqueue "k8s.io/client-go/util/workqueue"
	"k8s.io/sample-controller/pkg/signals"

	"github.com/google/uuid"

	"database/sql"
	_ "github.com/go-sql-driver/mysql" // must be blank; satisfies sql interface

	yaml "gopkg.in/yaml.v2"

	batchKube "github.com/akotlar/hail-go-batch/kubernetes"
	"github.com/davecgh/go-spew/spew"
)

var jsonFast = jsoniter.ConfigFastest

const app = "batch-job"

var instanceID = uuid.New().String()
var label = fmt.Sprintf("app=%s,hail.is/batch-instance=%s", app, instanceID)

const (
	Cancelled int16 = -2
	Created   int16 = -1
)

type dbConfig struct {
	DB struct {
		// https://github.com/go-sql-driver/mysql#dsn-data-source-name
		DSN string
	}
}

// func NewController(queue workqueue.RateLimitingInterface, indexer cache.Indexer, informer cache.Controller) *Controller {
// 	return &Controller{
// 		informer: informer,
// 		indexer:  indexer,
// 		queue:    queue,
// 	}
// }

// Jobs defines shared resources needed to manage pods
// contains a database handle and a map of prepared, commonly-used SQL statements
// These statements need to be closed after Jobs is no longer used, to prevent memory leaks
type Jobs struct {
	db         *sql.DB
	statements map[string]*sql.Stmt
	kubeClient *batchKube.KubeClient
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
	s["jobs.update.status.kube_id"], err = db.Prepare("UPDATE jobs.job SET status=? WHERE kube_id=?")
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
// TODO: consider deferring PodTemplateSpec, letting our kubernetes package handle it
func CreateJob(j *Jobs, jsonStr []byte) (*Job, error) {
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
			Labels: map[string]string{
				"app":                    app,
				"hail.is/batch-instance": instanceID,
				"uuid":                   kubeID,
			},
		},
		Spec: req.Spec,
	}

	res, err := j.kubeClient.Clientset.Pods(j.kubeClient.Namespace).Create(podTemplate)

	pJSON, err := jsonFast.Marshal(podTemplate)

	if err != nil {
		return nil, err
	}

	// 0 exit_status is the default; only makes sense in context of status == Complete / Cancelled
	result, err := j.statements["jobs.create"].Exec(
		kubeID, req.Attributes, req.Callback, pJSON, -2,
	)

	id, err := result.LastInsertId()

	if err != nil {
		return nil, err
	}

	// TODO: potentially combine into one compound query
	result, err = j.statements["job_batch.create"].Exec(
		id, *req.BatchID,
	)

	if err != nil {
		return nil, err
	}

	job := &Job{
		ID:          int(id),
		BatchID:     *req.BatchID,
		Attributes:  req.Attributes,
		PodTemplate: podTemplate,
		Callback:    req.Callback,
		// Job is created when Kubernetes successfully creates this job
		// TODO: Should we wait until
		ExitCode: Created,
	}

	return job, nil
}

// MarkJob updates the status of the job
func MarkJob(job *Job, id int, status int16) error {
	if status < Cancelled {
		return errors.New("Invalid status, expect -3 to 255")
	}

	return nil
}

// type Controller struct {
// 	workqueue workqueue.RateLimitingInterface
// 	podLister coreV1.PodLister
// }

// func WatchJobs(j *Jobs) {
// 	// create the pod watcher
// 	clientset := j.kubeClient.Clientset
// 	namespace := j.kubeClient.Namespace
// 	iFactory := j.kubeClient.SharedInformerFactory

// 	// set up signals so we handle the first shutdown signal gracefully
// 	stopCh := signals.SetupSignalHandler()

// 	// TODO: Investigate using cache.NewListWatch instead
// 	// podListWatcher := cache.NewListWatchFromClient(clientset.CoreV1().RESTClient(), "pods", namespace, fields.Everything())
// 	// podListWatcher.
// 	// create the workqueue
// 	queue := workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter())

// 	informer := iFactory.Core().V1().Pods()
// 	lister := informer.Lister()

// 	// eventBroadcaster.StartLogging(klog.Infof)
// 	// eventBroadcaster.StartRecordingToSink(&typedcorev1.EventSinkImpl{Interface: kubeclientset.CoreV1().Events("")})
// 	// recorder := eventBroadcaster.NewRecorder(scheme.Scheme, corev1.EventSource{Component: controllerAgentName})

// 	informer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
// 		AddFunc: func(obj interface{}) {
// 			log.Println("Got ADD event")
// 			spew.Dump("add", obj)
// 		},
// 		UpdateFunc: func(old, new interface{}) {
// 			log.Println("GOT UPDATE EVENT")
// 			spew.Dump("old", old)
// 			spew.Dump("new", new)
// 		}, DeleteFunc: func(obj interface{}) {
// 			log.Println("GOT DELETE EVENT")
// 			spew.Dump(obj)
// 		}})

// 	iFactory.Start(stopCh)

// if err = controller.Run(2, stopCh); err != nil {
// 	klog.Fatalf("Error running controller: %s", err.Error())
// }

// AddFunc: controller.handleObject,
// UpdateFunc: func(old, new interface{}) {
// 	newDepl := new.(*appsv1.Deployment)
// 	oldDepl := old.(*appsv1.Deployment)
// 	if newDepl.ResourceVersion == oldDepl.ResourceVersion {
// 		// Periodic resync will send update events for all known Deployments.
// 		// Two different versions of the same Deployment will always have different RVs.
// 		return
// 	}
// 	controller.handleObject(new)
// },
// DeleteFunc: controller.handleObject,

// stopper := make(chan struct{})
// defer close(stopper)

// informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
// 	AddFunc: func(obj interface{}) {
// 		mObj := obj.(v1.Object)
// 		log.Printf("New Pod Added to Store: %s", mObj.GetName())
// 	},
// })

// Bind the workqueue to a cache with the help of an informer. This way we make sure that
// whenever the cache is updated, the pod key is added to the workqueue.
// Note that when we finally process the item from the workqueue, we might see a newer version
// of the Pod than the version which was responsible for triggering the update.
// indexer, controller := cache.NewIndexerInformer(podListWatcher, &v1.Pod{}, 0, cache.ResourceEventHandlerFuncs{
// 	AddFunc: func(obj interface{}) {
// 		key, err := cache.MetaNamespaceKeyFunc(obj)
// 		if err == nil {
// 			queue.Add(key)
// 		}
// 	},
// 	UpdateFunc: func(old interface{}, new interface{}) {
// 		key, err := cache.MetaNamespaceKeyFunc(new)
// 		if err == nil {
// 			queue.Add(key)
// 		}
// 	},
// 	DeleteFunc: func(obj interface{}) {
// 		// IndexerInformer uses a delta queue, therefore for deletes we have to use this
// 		// key function.
// 		key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
// 		if err == nil {
// 			queue.Add(key)
// 		}
// 	},
// }, cache.Indexers{})

// controller := NewController(queue, indexer, informer)

// // We can now warm up the cache for initial synchronization.
// // Let's suppose that we knew about a pod "mypod" on our last run, therefore add it to the cache.
// // If this pod is not there anymore, the controller will be notified about the removal after the
// // cache has synchronized.
// indexer.Add(&v1.Pod{
// 	ObjectMeta: meta_v1.ObjectMeta{
// 		Name:      "mypod",
// 		Namespace: v1.NamespaceDefault,
// 	},
// })

// // Now let's start the controller
// stop := make(chan struct{})
// defer close(stop)
// go controller.Run(1, stop)

// // Wait forever
// select {}
// }

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

// Run will set up the event handlers for types we are interested in, as well
// as syncing informer caches and starting workers. It will block until stopCh
// is closed, at which point it will shutdown the workqueue and wait for
// workers to finish processing their current work items.
// func (c *Controller) Run(threadiness int, stopCh <-chan struct{}) error {
// 	defer utilruntime.HandleCrash()
// 	defer c.workqueue.ShutDown()

// 	// Start the informer factories to begin populating the informer caches
// 	klog.Info("Starting Foo controller")

// 	// Wait for the caches to be synced before starting workers
// 	klog.Info("Waiting for informer caches to sync")
// 	if ok := cache.WaitForCacheSync(stopCh, c.deploymentsSynced, c.foosSynced); !ok {
// 		return fmt.Errorf("failed to wait for caches to sync")
// 	}

// 	klog.Info("Starting workers")
// 	// Launch two workers to process Foo resources
// 	for i := 0; i < threadiness; i++ {
// 		go wait.Until(c.runWorker, time.Second, stopCh)
// 	}

// 	klog.Info("Started workers")
// 	<-stopCh
// 	klog.Info("Shutting down workers")

// 	return nil
// }
