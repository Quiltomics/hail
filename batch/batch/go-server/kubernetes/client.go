package kubernetes

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	jobs "github.com/akotlar/hail-go-batch/jobs"
	kubeErrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	// https://github.com/kubernetes/client-go/blob/53c7adfd0294caa142d961e1f780f74081d5b15f/examples/out-of-cluster-client-configuration/main.go#L31
	// import auth providers, needed for OIDC
	// avoids No Auth Provider found for name "gcp"
	// _ avoids "imported and not used"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	kubeRest "k8s.io/client-go/rest"
	kubeClientCmd "k8s.io/client-go/tools/clientcmd"

	"github.com/google/uuid"
)

type KubeClient struct {
	timeout    int //milliseconds
	namespace  string
	config     *kubeRest.Config
	clientset  *kubernetes.Clientset
	instanceID string
	label      string
}

// https://nathanleclaire.com/blog/2014/08/09/dont-get-bitten-by-pointer-vs-non-pointer-method-receivers-in-golang/
// https://golang.org/doc/faq#methods_on_values_or_pointers
func (k *KubeClient) DoStuff() {
	fmt.Print("Hello world")

}

// https://github.com/kubernetes/client-go/blob/master/examples/out-of-cluster-client-configuration/main.go
//
func New() *KubeClient {
	config, clientset, err := getClient()

	if err != nil {
		panic(err)
	}

	namespace := os.Getenv("POD_NAMESPACE")

	if namespace == "" {
		panic("Please set the environemt variable POD_NAMESPACE (e.g: export POD_NAMESPACE=test")
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

	instanceID := uuid.New().String()

	return &KubeClient{
		config:     config,
		clientset:  clientset,
		namespace:  namespace,
		timeout:    timeout,
		instanceID: instanceID,
		label:      fmt.Sprintf("app=batch-job,hail.is/batch-instance=%s", instanceID),
	}
}

func CreatePod(k *KubeClient, jobRequest []byte) error {
	// metadata=kube.client.V1ObjectMeta(generate_name='job-{}-'.format(self.id),
	//                                             labels={
	//                                                 'app': 'batch-job',
	//                                                 'hail.is/batch-instance': instance_id,
	//                                                 'uuid': uuid.uuid4().hex
	//           }),
	_, err := jobs.CreateJob(jobRequest)

	// pod, err = clientset.CoreV1().Pods(pod.Namespace).Create(pod)

	return err
}

func getClient() (config *kubeRest.Config, clientset *kubernetes.Clientset, err error) {
	useKube, err := parseBool(os.Getenv("BATCH_USE_KUBE_CONFIG"))

	if err != nil {
		panic(fmt.Sprintf("Invalid BATCH_USE_KUBE_CONFIG value %v", err))
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
		config, err = kubeClientCmd.BuildConfigFromFlags("", *kubeconfig)
	}

	if err != nil {
		return nil, nil, err
	}

	// creates the clientset
	clientset, err = kubernetes.NewForConfig(config)

	return config, clientset, err
}

func getPods(clientset *kubernetes.Clientset) {

	for {
		pods, err := clientset.CoreV1().Pods("").List(v1.ListOptions{})
		if err != nil {
			panic(err.Error())
		}
		fmt.Printf("There are %d pods in the cluster\n", len(pods.Items))

		// Examples for error handling:
		// - Use helper functions like e.g. errors.IsNotFound()
		// - And/or cast to StatusError and use its properties like e.g. ErrStatus.Message
		_, err = clientset.CoreV1().Pods("default").Get("example-xxxxx", v1.GetOptions{})
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

