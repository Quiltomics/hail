package kubernetes

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	kubeErrors "k8s.io/apimachinery/pkg/api/errors"
	metaV1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	coreV1 "k8s.io/api/core/v1"
	informers "k8s.io/client-go/informers"
	kubernetes "k8s.io/client-go/kubernetes"
	// https://github.com/kubernetes/client-go/blob/53c7adfd0294caa142d961e1f780f74081d5b15f/examples/out-of-cluster-client-configuration/main.go#L31
	// import auth providers, needed for OIDC
	// avoids No Auth Provider found for name "gcp"
	// _ avoids "imported and not used"
	kubeApi "golang.org/x/build/kubernetes/api"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	kubeRest "k8s.io/client-go/rest"
	clientcmd "k8s.io/client-go/tools/clientcmd"
)

// interface Jobs {
// 	OnAdded
// 	OnDeleted
// }

type KubeClient struct {
	Timeout               int //milliseconds
	Namespace             string
	Config                *kubeRest.Config
	Clientset             *kubernetes.Clientset
	SharedInformerFactory informers.SharedInformerFactory
}

// TODO: allow configuration per consumer of namespace, resync, etc
// Pass in YAML or use 12-factor-like env variables
// https://github.com/kubernetes/client-go/blob/master/examples/out-of-cluster-client-configuration/main.go
func New() (*KubeClient, error) {
	config, clientset, err := getClient()

	if err != nil {
		return nil, err
	}

	namespace := os.Getenv("POD_NAMESPACE") // maintain compatibility with existing server

	if namespace == "" {
		return nil, errors.New("Please set the environment variable POD_NAMESPACE (e.g: export POD_NAMESPACE=test")
	}

	var resyncTime time.Duration

	resyncTimeStr := os.Getenv("KUBERNETES_RESYNC_PERIOD")
	if resyncTimeStr == "" {
		resyncTime = time.Second * 60
	} else {
		seconds, err := strconv.Atoi(resyncTimeStr)

		if err != nil {
			return nil, err
		}

		resyncTime = time.Second * time.Duration(seconds)
	}

	timeoutStr := os.Getenv("KUBERNETES_TIMEOUT_IN_SECONDS")

	var timeout int
	if timeoutStr == "" {
		// Try to protect against Kubernetes long GC cyclec
		timeout = 120
	} else {
		timeout, err = strconv.Atoi(os.Getenv("KUBERNETES_TIMEOUT_IN_SECONDS"))

		if err != nil {
			panic(fmt.Sprintf("KUBERNETES_TIMEOUT_IN_SECONDS not a valid integer, got %s", timeoutStr))
		}
	}

	f := informers.NewSharedInformerFactoryWithOptions(clientset, resyncTime, informers.WithNamespace(namespace))

	return &KubeClient{
		Timeout:               timeout,
		Namespace:             namespace,
		Config:                config,
		Clientset:             clientset,
		SharedInformerFactory: f,
	}, nil
}

func CreatePod(k *KubeClient, podTemplate *kubeApi.PodTemplateSpec) {
	// res, err := k.Clientset.CoreV1().Pods(k.Namespace).Create(&coreV1.Pod{
	// 	ObjectMeta: podTemplate.ObjectMeta,
	// 	Spec:       podTemplate.Spec,
	// })
	// TODO: This seems like a waste of effort, but coreV1.Pod contains state as
	// well
	pod := coreV1.Pod{
		ObjectMeta: podTemplate.ObjectMeta,
		Spec:       podTemplate.Spec,
	}
}

func getClient() (config *kubeRest.Config, clientset *kubernetes.Clientset, err error) {
	useKube, err := parseBool(os.Getenv("BATCH_USE_KUBE_CONFIG"))

	if err != nil {
		return nil, nil, err
	}

	if useKube != false {
		config, err = kubeRest.InClusterConfig()
	} else {
		var kubeconfig *string
		if home := homeDir(); home != "" {
			kubeconfig = flag.String("kubeconfig", filepath.Join(home, ".kube", "config"), "(optional) absolute path to the kubeconfig file")
		} else {
			kubeconfig = flag.String("kubeconfig", "", "absolute path to the kubeconfig file")
		}
		flag.Parse()

		// use the current context in kubeconfig
		config, err = clientcmd.BuildConfigFromFlags("", *kubeconfig)
	}

	if err != nil {
		return nil, nil, err
	}

	// creates the clientset
	clientset, err = kubernetes.NewForConfig(config)

	return config, clientset, err
}

func getPods(k *KubeClient) {

	for {
		pods, err := k.Clientset.CoreV1().Pods("").List(metaV1.ListOptions{})
		if err != nil {
			panic(err.Error())
		}
		fmt.Printf("There are %d pods in the cluster\n", len(pods.Items))

		// Examples for error handling:
		// - Use helper functions like e.g. errors.IsNotFound()
		// - And/or cast to StatusError and use its properties like e.g. ErrStatus.Message
		_, err = k.Clientset.CoreV1().Pods("default").Get("example-xxxxx", metaV1.GetOptions{})
		if kubeErrors.IsNotFound(err) {
			fmt.Printf("Pod not found\n")
		} else if statusError, isStatus := err.(*kubeErrors.StatusError); isStatus {
			fmt.Printf("Error getting pod %v\n", statusError.ErrStatus.Message)
		} else if err != nil {
			panic(err.Error())
		} else {
			fmt.Printf("Found pod\n")
		}

		time.Sleep(10 * time.Second)
	}
}

func parseBool(str string) (bool, error) {
	switch str {
	case "", "0", "F", "False", "FALSE":
		return false, nil
	case "1", "T", "True", "TRUE":
		return true, nil
	}
	return false, fmt.Errorf("Got: %s. Must be one of '0', 'F', 'False', 'FALSE', '1', 'T', 'True', 'TRUE'", str)
}

// https://github.com/kubernetes/client-go/blob/master/examples/out-of-cluster-client-configuration/main.go
func homeDir() string {
	if h := os.Getenv("HOME"); h != "" {
		return h
	}
	return os.Getenv("USERPROFILE") // windows
}

// //adapted from https://github.com/dtan4/k8s-pod-notifier/blob/master/kubernetes/client.go
// // https://github.com/kubernetes/client-go/blob/master/tools/cache/listwatch.go
// // https://medium.com/programming-kubernetes/building-stuff-with-the-kubernetes-api-part-4-using-go-b1d0e3c1c899

// // TODO: Add cache
// // https://github.com/kubernetes/client-go/tree/master/examples/workqueue
// package kubernetes

// import (
// 	"context"

// 	"github.com/pkg/errors"
// 	log "github.com/sirupsen/logrus"
// 	"k8s.io/apimachinery/pkg/watch"
// 	"k8s.io/apimachinery/pkg/apis/meta/v1"

// 	"github.com/akotlar/hail-go-batch/kubernetes/jobs"
// )

// // WatchPodEvents watches Pod events
// func (c *KubeClient, j *jobs.Jobs) WatchPodEvents(namespace, labels string, notifySuccess, notifyFail bool, succeededFunc, failedFunc NotifyFunc) error {
// 	watcher, err := c.clientset.CoreV1().Pods(c.namespace).Watch(v1.ListOptions{
// 		LabelSelector: labels,
// 	})
// 	if err != nil {
// 		return errors.Wrap(err, "cannot create Pod event watcher")
// 	}

// 	go func() {
// 		for {
// 			select {
// 			case e := <-watcher.ResultChan():
// 				if e.Object == nil {
// 					return
// 				}

// 				pod, ok := e.Object.(*v1.Pod)
// 				if !ok {
// 					continue
// 				}

// 				log.WithFields(log.Fields{
// 					"action":     e.Type,
// 					"namespace":  pod.Namespace,
// 					"name":       pod.Name,
// 					"phase":      pod.Status.Phase,
// 					"reason":     pod.Status.Reason,
// 					"container#": len(pod.Status.ContainerStatuses),
// 				}).Debug("event notified")

// 				switch e.Type {
// 				case watch.Modified:
// 					if pod.DeletionTimestamp != nil {
// 						continue
// 					}

// 					startedAt := pod.CreationTimestamp.Time

// 					switch pod.Status.Phase {
// 					case v1.PodSucceeded:
// 						for _, cst := range pod.Status.ContainerStatuses {
// 							if cst.State.Terminated == nil {
// 								continue
// 							}

// 							finishedAt := cst.State.Terminated.FinishedAt.Time

// 							if cst.State.Terminated.Reason == "Completed" {
// 								if notifySuccess {
// 									succeededFunc(&PodEvent{
// 										Namespace:  pod.Namespace,
// 										PodName:    pod.Name,
// 										StartedAt:  startedAt,
// 										FinishedAt: finishedAt,
// 										ExitCode:   0,
// 										Reason:     "",
// 										Message:    "",
// 									})
// 								}
// 							} else {
// 								if notifyFail {
// 									failedFunc(&PodEvent{
// 										Namespace:  pod.Namespace,
// 										PodName:    pod.Name,
// 										StartedAt:  startedAt,
// 										FinishedAt: finishedAt,
// 										ExitCode:   int(cst.State.Terminated.ExitCode),
// 										Reason:     cst.State.Terminated.Reason,
// 										Message:    "",
// 									})
// 								}
// 							}

// 							break
// 						}
// 					case v1.PodFailed:
// 						if len(pod.Status.ContainerStatuses) == 0 { // e.g. Pod was evicted
// 							if notifyFail {
// 								failedFunc(&PodEvent{
// 									Namespace:  pod.Namespace,
// 									PodName:    pod.Name,
// 									StartedAt:  startedAt,
// 									FinishedAt: startedAt,
// 									ExitCode:   -1,
// 									Reason:     pod.Status.Reason,
// 									Message:    pod.Status.Message,
// 								})
// 							}
// 						} else {
// 							for _, cst := range pod.Status.ContainerStatuses {
// 								if cst.State.Terminated == nil {
// 									continue
// 								}

// 								if notifyFail {
// 									finishedAt := cst.State.Terminated.FinishedAt.Time

// 									failedFunc(&PodEvent{
// 										Namespace:  pod.Namespace,
// 										PodName:    pod.Name,
// 										StartedAt:  startedAt,
// 										FinishedAt: finishedAt,
// 										ExitCode:   int(cst.State.Terminated.ExitCode),
// 										Reason:     cst.State.Terminated.Reason,
// 										Message:    pod.Status.Message,
// 									})
// 								}

// 								break
// 							}
// 						}
// 					default:
// 						for _, cst := range pod.Status.ContainerStatuses {
// 							if cst.State.Terminated == nil {
// 								continue
// 							}

// 							if notifyFail {
// 								finishedAt := cst.State.Terminated.FinishedAt.Time

// 								failedFunc(&PodEvent{
// 									Namespace:  pod.Namespace,
// 									PodName:    pod.Name,
// 									StartedAt:  startedAt,
// 									FinishedAt: finishedAt,
// 									ExitCode:   int(cst.State.Terminated.ExitCode),
// 									Reason:     cst.State.Terminated.Reason,
// 									Message:    pod.Status.Message,
// 								})
// 							}

// 							break
// 						}
// 					}
// 				}
// 			case <-ctx.Done():
// 				watcher.Stop()
// 				return
// 			}
// 		}
// 	}()

// 	return nil
// }

