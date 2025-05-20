package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
)

// Version is the application version, set during build time using ldflags
var Version = "dev"

// Args holds the command line arguments for the application
type Args struct {
	PodName         string
	DeploymentName  string
	StatefulSetName string
	Namespace       string
	ContainerName   string
	SearchPattern   string
	TimeoutSecs     int
	Debug           bool
	Help            bool
	ShowVersion     bool
	KubeConfig      string
	KubeContext     string
}

// ResourceType represents the type of Kubernetes resource
type ResourceType string

// Constants for resource types
const (
	ResourceTypeDeployment  ResourceType = "deployment"
	ResourceTypeStatefulSet ResourceType = "statefulset"
)

// PodSearchResult stores the result of searching a single pod
type PodSearchResult struct {
	PodName string
	Found   bool
	Error   error
}

func main() {
	// Parse command line arguments
	args := parseArgs()

	// Show version if requested
	if args.ShowVersion {
		fmt.Printf("klogs-needle version %s\n", Version)
		os.Exit(0)
	}

	// Show help if requested
	if args.Help {
		flag.Usage()
		os.Exit(0)
	}

	// Validate required arguments
	if err := validateArgs(args); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		flag.Usage()
		os.Exit(1)
	}

	// Create Kubernetes client
	clientset, err := createK8sClient(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating Kubernetes client: %v\n", err)
		os.Exit(1)
	}

	// Set up context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(args.TimeoutSecs)*time.Second)
	defer cancel()

	// Search for the pattern in pod logs
	found, err := searchPodLogs(ctx, clientset, args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(2)
	}

	if found {
		if args.PodName != "" {
			fmt.Printf("Success: Found pattern '%s' in logs of pod %s\n", args.SearchPattern, args.PodName)
		} else {
			var resourceType ResourceType
			var resourceName string

			if args.DeploymentName != "" {
				resourceType = ResourceTypeDeployment
				resourceName = args.DeploymentName
			} else {
				resourceType = ResourceTypeStatefulSet
				resourceName = args.StatefulSetName
			}

			fmt.Printf("Success: Found pattern '%s' in logs of all active pods in %s %s\n",
				args.SearchPattern, resourceType, resourceName)
		}
		os.Exit(0)
	} else {
		// Timeout or pattern not found
		if args.PodName != "" {
			fmt.Fprintf(os.Stderr, "Timeout: Pattern '%s' not found in logs of pod %s within %d seconds\n",
				args.SearchPattern, args.PodName, args.TimeoutSecs)
		} else {
			var resourceType ResourceType
			var resourceName string

			if args.DeploymentName != "" {
				resourceType = ResourceTypeDeployment
				resourceName = args.DeploymentName
			} else {
				resourceType = ResourceTypeStatefulSet
				resourceName = args.StatefulSetName
			}

			fmt.Fprintf(os.Stderr, "Timeout: Pattern '%s' not found in logs of all active pods in %s %s within %d seconds\n",
				args.SearchPattern, resourceType, resourceName, args.TimeoutSecs)
		}
		os.Exit(3)
	}
}

// Parse command line arguments
func parseArgs() Args {
	args := Args{}

	// Default kubeconfig path
	var defaultKubeconfig string
	if home := homedir.HomeDir(); home != "" {
		defaultKubeconfig = filepath.Join(home, ".kube", "config")
	}

	flag.StringVar(&args.PodName, "pod", "", "Pod name (required if deployment and statefulset not specified)")
	flag.StringVar(&args.DeploymentName, "deployment", "", "Deployment name (required if pod and statefulset not specified)")
	flag.StringVar(&args.StatefulSetName, "statefulset", "", "StatefulSet name (required if pod and deployment not specified)")
	flag.StringVar(&args.Namespace, "namespace", "default", "Kubernetes namespace")
	flag.StringVar(&args.ContainerName, "container", "", "Container name (optional if pod has only one container)")
	flag.StringVar(&args.SearchPattern, "needle", "", "Search string/pattern to look for in logs (required)")
	flag.IntVar(&args.TimeoutSecs, "timeout", 60, "Timeout in seconds (optional)")
	flag.BoolVar(&args.Debug, "debug", false, "Enable debug mode to print logs")
	flag.StringVar(&args.KubeConfig, "kubeconfig", defaultKubeconfig, "Path to kubeconfig file (optional, defaults to ~/.kube/config)")
	flag.StringVar(&args.KubeContext, "context", "", "Kubernetes context to use (optional)")
	help := flag.Bool("help", false, "Show help")
	h := flag.Bool("h", false, "Show help")
	version := flag.Bool("version", false, "Show version information")
	v := flag.Bool("v", false, "Show version information")

	// Define custom usage message
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [options]\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "klogs-needle monitors Kubernetes pod logs for a specific string pattern.\n\n")
		fmt.Fprintf(os.Stderr, "Options:\n")
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nExamples:\n")
		fmt.Fprintf(os.Stderr, "  %s -pod my-pod -namespace my-namespace -needle \"Service started\" -timeout 60\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s -deployment my-deployment -namespace my-namespace -needle \"Service started\" -timeout 60\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s -statefulset my-statefulset -namespace my-namespace -needle \"Service started\" -timeout 60\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s -pod my-pod -kubeconfig /path/to/kubeconfig -context my-context -needle \"Service started\"\n", os.Args[0])
	}

	flag.Parse()

	// Check for help flag
	args.Help = *help || *h

	// Check for version flag
	args.ShowVersion = *version || *v

	return args
}

// Validate required arguments
func validateArgs(args Args) error {
	// Skip validation if showing version or help
	if args.ShowVersion || args.Help {
		return nil
	}

	// Check if at least one resource type is specified
	if args.PodName == "" && args.DeploymentName == "" && args.StatefulSetName == "" {
		return fmt.Errorf("either pod name, deployment name, or statefulset name is required")
	}

	// Check that only one resource type is specified
	specifiedCount := 0
	if args.PodName != "" {
		specifiedCount++
	}
	if args.DeploymentName != "" {
		specifiedCount++
	}
	if args.StatefulSetName != "" {
		specifiedCount++
	}

	if specifiedCount > 1 {
		return fmt.Errorf("cannot specify more than one of: pod name, deployment name, statefulset name")
	}

	// Validate other required arguments
	if args.SearchPattern == "" {
		return fmt.Errorf("search pattern (needle) is required")
	}
	if args.TimeoutSecs <= 0 {
		return fmt.Errorf("timeout must be a positive number of seconds")
	}
	return nil
}

// Create Kubernetes client using in-cluster or out-of-cluster configuration
func createK8sClient(args Args) (*kubernetes.Clientset, error) {
	var config *rest.Config
	var err error

	// Try in-cluster config first
	config, err = rest.InClusterConfig()
	if err != nil {
		// If in-cluster config fails, try using kubeconfig file
		fmt.Println("Not running inside a Kubernetes cluster, using local kubeconfig")

		// Check if kubeconfig file exists
		if _, err := os.Stat(args.KubeConfig); os.IsNotExist(err) {
			return nil, fmt.Errorf("kubeconfig file not found at %s: %v", args.KubeConfig, err)
		}

		// Load kubeconfig
		loadingRules := &clientcmd.ClientConfigLoadingRules{ExplicitPath: args.KubeConfig}
		configOverrides := &clientcmd.ConfigOverrides{}

		// Set context if provided
		if args.KubeContext != "" {
			configOverrides.CurrentContext = args.KubeContext
		}

		kubeConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, configOverrides)
		config, err = kubeConfig.ClientConfig()
		if err != nil {
			return nil, fmt.Errorf("failed to load kubeconfig: %v", err)
		}
	} else {
		fmt.Println("Running inside a Kubernetes cluster, using in-cluster configuration")
	}

	// Create clientset
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create Kubernetes client: %v", err)
	}

	return clientset, nil
}

// Search for pattern in pod logs
func searchPodLogs(ctx context.Context, clientset *kubernetes.Clientset, args Args) (bool, error) {
	if args.PodName != "" {
		// Search in a single pod
		return searchSinglePodLogs(ctx, clientset, args.PodName, args)
	}
	if args.DeploymentName != "" {
		// Search in all pods of a deployment
		return searchResourcePodLogs(ctx, clientset, ResourceTypeDeployment, args.DeploymentName, args)
	}
	// Search in all pods of a statefulset
	return searchResourcePodLogs(ctx, clientset, ResourceTypeStatefulSet, args.StatefulSetName, args)
}

// Search for pattern in logs of all pods in a resource (deployment or statefulset)
func searchResourcePodLogs(ctx context.Context, clientset *kubernetes.Clientset, resourceType ResourceType, resourceName string, args Args) (bool, error) {
	// Get pods from the resource
	var pods []corev1.Pod
	var err error

	switch resourceType {
	case ResourceTypeDeployment:
		pods, err = getPodsFromDeployment(ctx, clientset, resourceName, args.Namespace)
	case ResourceTypeStatefulSet:
		pods, err = getPodsFromStatefulSet(ctx, clientset, resourceName, args.Namespace)
	default:
		return false, fmt.Errorf("unsupported resource type: %s", resourceType)
	}

	if err != nil {
		return false, err
	}

	fmt.Printf("Found %d pods for %s '%s'\n", len(pods), resourceType, resourceName)

	// Create a wait group to wait for all goroutines
	var wg sync.WaitGroup
	// Create a mutex for synchronizing access to shared resources
	var mu sync.Mutex
	// Create a channel to receive results
	resultChan := make(chan PodSearchResult, len(pods))
	// Create a channel to signal early termination
	doneChan := make(chan struct{})
	// Use atomic counters for thread safety
	var successCount int32
	var errorCount int32
	podCount := len(pods)

	// Create a context that will be canceled when the first pod finds the pattern or on timeout
	searchCtx, cancelSearch := context.WithCancel(ctx)
	defer cancelSearch() // Ensure context is canceled when we exit

	// Start a goroutine for each pod
	for _, pod := range pods {
		wg.Add(1)
		go func(pod corev1.Pod) {
			// Ensure WaitGroup is decremented even if panic occurs
			defer func() {
				if r := recover(); r != nil {
					mu.Lock()
					fmt.Fprintf(os.Stderr, "Panic in goroutine for pod '%s': %v\n%s\n",
						pod.Name, r, debug.Stack())
					mu.Unlock()

					// Send error result to channel
					select {
					case resultChan <- PodSearchResult{
						PodName: pod.Name,
						Found:   false,
						Error:   fmt.Errorf("panic occurred: %v", r),
					}:
					case <-searchCtx.Done():
						// Context was canceled, don't send to channel
					}
				}
				wg.Done()
			}()

			// Create a timeout specific to this goroutine
			podCtx, podCancel := context.WithTimeout(searchCtx, time.Duration(args.TimeoutSecs)*time.Second)
			defer podCancel() // Ensure the context is canceled when goroutine exits

			// Create args for this pod
			podArgs := args
			podArgs.PodName = pod.Name

			// Search for pattern in this pod
			found, err := searchSinglePodLogs(podCtx, clientset, pod.Name, podArgs)

			// Check if context was canceled before sending result
			select {
			case <-searchCtx.Done():
				// Context was canceled, don't send to channel
				return
			default:
				// Send result to channel
				resultChan <- PodSearchResult{
					PodName: pod.Name,
					Found:   found,
					Error:   err,
				}

				// If pattern was found, cancel the context to stop other goroutines
				if found && atomic.AddInt32(&successCount, 1) == int32(podCount) {
					// All pods have found the pattern, signal early termination
					select {
					case doneChan <- struct{}{}:
					default:
						// Channel already has a value, no need to send again
					}
					cancelSearch()
				}
			}
		}(pod)
	}

	// Close the result channel when all goroutines are done
	go func() {
		wg.Wait()
		close(resultChan)
		close(doneChan)
	}()

	// Process results
	for {
		select {
		case <-ctx.Done():
			// Parent context was canceled (timeout)
			return false, nil

		case <-doneChan:
			// All pods have found the pattern
			return true, nil

		case result, ok := <-resultChan:
			if !ok {
				// Channel closed, all goroutines are done
				// Check final counts
				finalSuccessCount := atomic.LoadInt32(&successCount)
				finalErrorCount := atomic.LoadInt32(&errorCount)

				if finalSuccessCount == int32(podCount) {
					return true, nil
				}

				if finalErrorCount > 0 {
					return false, fmt.Errorf("failed to search logs in %d out of %d pods",
						finalErrorCount, podCount)
				}

				return false, nil
			}

			// Process the result
			if result.Error != nil {
				mu.Lock()
				fmt.Fprintf(os.Stderr, "Error searching pod '%s': %v\n", result.PodName, result.Error)
				mu.Unlock()
				atomic.AddInt32(&errorCount, 1)
			} else if result.Found {
				// Success count is incremented in the goroutine when found
			}

			// Check if we're done due to errors or success
			totalProcessed := atomic.LoadInt32(&errorCount) + atomic.LoadInt32(&successCount)
			if totalProcessed == int32(podCount) {
				// All pods have been processed
				if atomic.LoadInt32(&errorCount) > 0 {
					// Some pods had errors
					return false, fmt.Errorf("failed to search logs in %d out of %d pods",
						atomic.LoadInt32(&errorCount), podCount)
				}

				// All pods were processed successfully
				if atomic.LoadInt32(&successCount) == int32(podCount) {
					// All pods found the pattern
					return true, nil
				}

				// Some pods didn't find the pattern (but had no errors)
				return false, nil
			}
		}
	}
}

// Get pods from a deployment
func getPodsFromDeployment(ctx context.Context, clientset *kubernetes.Clientset, deploymentName, namespace string) ([]corev1.Pod, error) {
	// Get the deployment
	deployment, err := clientset.AppsV1().Deployments(namespace).Get(ctx, deploymentName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to find deployment '%s' in namespace '%s': %v", deploymentName, namespace, err)
	}

	// Explicitly use appsv1 type to avoid unused import
	var _ appsv1.Deployment = appsv1.Deployment{}

	// Get the selector from the deployment
	selector := deployment.Spec.Selector
	labelSelector := labels.SelectorFromSet(selector.MatchLabels)

	// List pods with the selector
	pods, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector.String(),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list pods for deployment '%s': %v", deploymentName, err)
	}

	// Get the ReplicaSet that's currently owned by the deployment
	replicaSets, err := clientset.AppsV1().ReplicaSets(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector.String(),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list ReplicaSets for deployment '%s': %v", deploymentName, err)
	}

	// Find the active ReplicaSet (the one with the most replicas)
	var activeReplicaSet *appsv1.ReplicaSet
	for i := range replicaSets.Items {
		rs := &replicaSets.Items[i]
		// Check if this ReplicaSet is owned by our deployment
		for _, owner := range rs.OwnerReferences {
			if owner.Kind == "Deployment" && owner.Name == deploymentName {
				if activeReplicaSet == nil || *rs.Spec.Replicas > *activeReplicaSet.Spec.Replicas {
					activeReplicaSet = rs
				}
				break
			}
		}
	}

	if activeReplicaSet == nil {
		return nil, fmt.Errorf("no active ReplicaSet found for deployment '%s'", deploymentName)
	}

	// Filter pods to only include those from the active ReplicaSet and not terminating
	activePods := []corev1.Pod{}
	for _, pod := range pods.Items {
		// Skip pods that are being deleted
		if pod.DeletionTimestamp != nil {
			fmt.Printf("Skipping terminating pod '%s' (has deletion timestamp)\n", pod.Name)
			continue
		}

		// Skip pods that are not in Running phase
		if pod.Status.Phase != corev1.PodRunning {
			fmt.Printf("Skipping non-running pod '%s' (phase: %s)\n", pod.Name, pod.Status.Phase)
			continue
		}

		// Check if this pod is owned by the active ReplicaSet
		isOwnedByActiveRS := false
		for _, owner := range pod.OwnerReferences {
			if owner.Kind == "ReplicaSet" && owner.Name == activeReplicaSet.Name {
				isOwnedByActiveRS = true
				break
			}
		}

		if !isOwnedByActiveRS {
			fmt.Printf("Skipping pod '%s' (not owned by the active ReplicaSet '%s')\n", pod.Name, activeReplicaSet.Name)
			continue
		}

		activePods = append(activePods, pod)
	}

	if len(activePods) == 0 {
		return nil, fmt.Errorf("no active pods found for deployment '%s'", deploymentName)
	}

	fmt.Printf("Found %d active pods from ReplicaSet '%s' for deployment '%s'\n",
		len(activePods), activeReplicaSet.Name, deploymentName)
	return activePods, nil
}

// Get pods from a statefulset
func getPodsFromStatefulSet(ctx context.Context, clientset *kubernetes.Clientset, statefulSetName, namespace string) ([]corev1.Pod, error) {
	// Get the statefulset
	statefulSet, err := clientset.AppsV1().StatefulSets(namespace).Get(ctx, statefulSetName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to find statefulset '%s' in namespace '%s': %v", statefulSetName, namespace, err)
	}

	// Get the selector from the statefulset
	selector := statefulSet.Spec.Selector
	labelSelector := labels.SelectorFromSet(selector.MatchLabels)

	// List pods with the selector
	pods, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector.String(),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list pods for statefulset '%s': %v", statefulSetName, err)
	}

	// Get the current revision and update revision from the StatefulSet status
	currentRevision := statefulSet.Status.CurrentRevision
	updateRevision := statefulSet.Status.UpdateRevision

	// If updateRevision is set and different from currentRevision, a rolling update is in progress
	isRollingUpdate := updateRevision != "" && updateRevision != currentRevision

	if isRollingUpdate {
		fmt.Printf("StatefulSet '%s' is undergoing a rolling update (current: %s, update: %s)\n",
			statefulSetName, currentRevision, updateRevision)
	}

	// Filter out terminating pods and ensure they belong to the StatefulSet
	activePods := []corev1.Pod{}
	for _, pod := range pods.Items {
		// Skip pods that are being deleted
		if pod.DeletionTimestamp != nil {
			fmt.Printf("Skipping terminating pod '%s' (has deletion timestamp)\n", pod.Name)
			continue
		}

		// Skip pods that are not in Running phase
		if pod.Status.Phase != corev1.PodRunning {
			fmt.Printf("Skipping non-running pod '%s' (phase: %s)\n", pod.Name, pod.Status.Phase)
			continue
		}

		// Check if this pod is owned by the StatefulSet
		isOwnedByStatefulSet := false
		for _, owner := range pod.OwnerReferences {
			if owner.Kind == "StatefulSet" && owner.Name == statefulSetName {
				isOwnedByStatefulSet = true
				break
			}
		}

		if !isOwnedByStatefulSet {
			fmt.Printf("Skipping pod '%s' (not owned by the StatefulSet '%s')\n", pod.Name, statefulSetName)
			continue
		}

		// If a rolling update is in progress, check the pod's controller-revision-hash label
		if isRollingUpdate {
			// Get the controller-revision-hash label
			revisionHash, ok := pod.Labels["controller-revision-hash"]
			if !ok {
				fmt.Printf("Skipping pod '%s' (missing controller-revision-hash label)\n", pod.Name)
				continue
			}

			// During a rolling update, we want to include only pods with the update revision
			if revisionHash != updateRevision {
				fmt.Printf("Skipping pod '%s' (old revision: %s, target: %s)\n",
					pod.Name, revisionHash, updateRevision)
				continue
			}
		}

		activePods = append(activePods, pod)
	}

	if len(activePods) == 0 {
		return nil, fmt.Errorf("no active pods found for statefulset '%s'", statefulSetName)
	}

	fmt.Printf("Found %d active pods for StatefulSet '%s'\n", len(activePods), statefulSetName)
	return activePods, nil
}

// Search for pattern in logs of a single pod
func searchSinglePodLogs(ctx context.Context, clientset *kubernetes.Clientset, podName string, args Args) (bool, error) {
	// Check if pod exists
	pod, err := clientset.CoreV1().Pods(args.Namespace).Get(ctx, podName, metav1.GetOptions{})
	if err != nil {
		return false, fmt.Errorf("failed to find pod '%s' in namespace '%s': %v", podName, args.Namespace, err)
	}

	// Skip terminating pods
	if pod.DeletionTimestamp != nil {
		return false, fmt.Errorf("pod '%s' is being terminated (has deletion timestamp), skipping log search", podName)
	}

	if pod.Status.Phase != corev1.PodRunning {
		return false, fmt.Errorf("pod '%s' is not running (phase: %s), skipping log search", podName, pod.Status.Phase)
	}

	// Validate container name if provided
	if args.ContainerName != "" {
		containerExists := false
		for _, container := range pod.Spec.Containers {
			if container.Name == args.ContainerName {
				containerExists = true
				break
			}
		}
		if !containerExists {
			return false, fmt.Errorf("container '%s' not found in pod '%s'", args.ContainerName, podName)
		}
	} else if len(pod.Spec.Containers) > 1 {
		// If container name is not provided and pod has multiple containers
		containerNames := []string{}
		for _, container := range pod.Spec.Containers {
			containerNames = append(containerNames, container.Name)
		}
		return false, fmt.Errorf("pod '%s' has multiple containers (%s), please specify a container name",
			podName, strings.Join(containerNames, ", "))
	}

	// Set up log options
	podLogOptions := corev1.PodLogOptions{
		Follow:    true,
		Container: args.ContainerName,
	}

	// Request logs
	req := clientset.CoreV1().Pods(args.Namespace).GetLogs(podName, &podLogOptions)
	podLogs, err := req.Stream(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to open log stream for pod '%s': %v", podName, err)
	}
	defer podLogs.Close()

	// Read logs line by line
	reader := bufio.NewReader(podLogs)
	for {
		select {
		case <-ctx.Done():
			// Timeout reached
			return false, nil
		default:
			line, err := reader.ReadString('\n')
			if err != nil {
				// Check if context was canceled (timeout)
				if ctx.Err() != nil {
					return false, nil
				}
				return false, fmt.Errorf("error reading logs: %v", err)
			}

			// Print log line if debug is enabled
			if args.Debug {
				fmt.Printf("[%s] %s", podName, line)
			}

			// Check if line contains the search pattern
			if strings.Contains(line, args.SearchPattern) {
				if args.Debug || args.DeploymentName != "" || args.StatefulSetName != "" {
					fmt.Printf("Found pattern '%s' in pod '%s'\n", args.SearchPattern, podName)
				}
				return true, nil
			}
		}
	}
}
