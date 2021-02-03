// Copyright (c) 2021 Red Hat, Inc.

package automation

import (
	"context"
	"fmt"

	"github.com/ghodss/yaml"
	policiesv1 "github.com/open-cluster-management/governance-policy-propagator/pkg/apis/policies/v1"
	"github.com/open-cluster-management/governance-policy-propagator/pkg/controller/common"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

const controllerName string = "policy-automation"

var log = logf.Log.WithName(controllerName)

/**
* USER ACTION REQUIRED: This is a scaffold file intended for the user to modify with their own Controller
* business logic.  Delete these comments after modifying this file.*
 */

// Add creates a new Policy Controller and adds it to the Manager. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(mgr manager.Manager) error {
	return add(mgr, newReconciler(mgr))
}

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager) reconcile.Reconciler {
	dyamicClient, err := dynamic.NewForConfig(mgr.GetConfig())
	if err != nil {
		panic(err)
	}
	return &ReconcilePolicy{client: mgr.GetClient(), scheme: mgr.GetScheme(), dyamicClient: dyamicClient,
		recorder: mgr.GetEventRecorderFor(controllerName)}
}

// add adds a new Controller to mgr with r as the reconcile.Reconciler
func add(mgr manager.Manager, r reconcile.Reconciler) error {
	// Create a new controller
	c, err := controller.New(controllerName, mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	// Watch for changes to primary resource Policy
	err = c.Watch(&source.Kind{Type: &policiesv1.Policy{}}, &handler.EnqueueRequestForObject{}, policyPredicateFuncs)
	if err != nil {
		return err
	}

	// Watch for changes to config map
	err = c.Watch(&source.Kind{Type: &corev1.ConfigMap{}},
		&common.EnqueueRequestsFromMapFunc{ToRequests: &configMapMapper{mgr.GetClient()}},
		configMapPredicateFuncs)
	if err != nil {
		return err
	}

	return nil
}

// blank assignment to verify that ReconcilePolicy implements reconcile.Reconciler
var _ reconcile.Reconciler = &ReconcilePolicy{}

// ReconcilePolicy reconciles a Policy object
type ReconcilePolicy struct {
	// This client, initialized using mgr.Client() above, is a split client
	// that reads objects from the cache and writes to the apiserver
	client       client.Client
	dyamicClient dynamic.Interface
	scheme       *runtime.Scheme
	recorder     record.EventRecorder
	counter      int
}

// Reconcile reads that state of the cluster for a Policy object and makes changes based on the state read
// and what is in the Policy.Spec
// Note:
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *ReconcilePolicy) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	reqLogger := log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)

	// Fetch the Policy instance
	instance := &policiesv1.Policy{}
	err := r.client.Get(context.TODO(), request.NamespacedName, instance)
	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected.

			// reqLogger.Info("Policy clean up complete, reconciliation completed.")
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return reconcile.Result{}, err
	}
	cfgMapList := &corev1.ConfigMapList{}
	err = r.client.List(context.TODO(), cfgMapList, &client.ListOptions{Namespace: request.Namespace})
	if err != nil {
		return reconcile.Result{}, err
	}
	for _, cfgMap := range cfgMapList.Items {
		if cfgMap.Data["policyRef"] == request.Name {
			log.Info("Excuting automation from configmap ...",
				"Namespace", cfgMap.GetNamespace(), "Name", cfgMap.GetName(), "Policy-Name", request.Name)
			if cfgMap.Annotations["policy.open-cluster-management.io/run-immediately"] == "true" {
				log.Info("Triggering single run from configmap ...",
					"Namespace", cfgMap.GetNamespace(), "Name", cfgMap.GetName(), "Policy-Name", request.Name)
			}
			delete(cfgMap.Annotations, "policy.open-cluster-management.io/run-immediately")
			r.client.Update(context.TODO(), &cfgMap, &client.UpdateOptions{})
			ansibleJob := &unstructured.Unstructured{}
			err := yaml.Unmarshal([]byte(cfgMap.Data["ansible.yaml"]), ansibleJob)
			if err != nil {
				log.Error(err, "error found!")
			}
			log.Info("", "ansibleJob", ansibleJob.Object["spec"])
			ansibleJobRes := schema.GroupVersionResource{Group: "tower.ansible.com", Version: "v1alpha1", Resource: "ansiblejobs"}
			_, err = r.dyamicClient.Resource(ansibleJobRes).Namespace(request.Namespace).Create(context.TODO(), ansibleJob, v1.CreateOptions{})
			if err != nil {
				log.Error(err, "error found!")
			}
		}
	}
	r.counter++
	reqLogger.Info(fmt.Sprintf("%d", r.counter))
	reqLogger.Info("Policy reconciliation completed.")
	// requeueAfter, _ := time.ParseDuration("60s")
	return reconcile.Result{RequeueAfter: 0}, nil
}
