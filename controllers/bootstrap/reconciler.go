/*
Copyright 2023 The Nephio Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package bootstrap

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/henderiw-nephio/nephio-controllers/controllers"
	ctrlconfig "github.com/henderiw-nephio/nephio-controllers/controllers/config"
	"github.com/henderiw-nephio/nephio-controllers/pkg/cluster"
	"github.com/nephio-project/nephio/controllers/pkg/resource"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func init() {
	controllers.Register("bootstrap", &reconciler{})
}

//+kubebuilder:rbac:groups="*",resources=secrets,verbs=get;list;watch
//+kubebuilder:rbac:groups=cluster.x-k8s.io,resources=clusters,verbs=get;list;watch
//+kubebuilder:rbac:groups=cluster.x-k8s.io,resources=clusters/status,verbs=get
//+kubebuilder:rbac:groups=porch.kpt.dev,resources=packagerevisions,verbs=get;list;watch
//+kubebuilder:rbac:groups=porch.kpt.dev,resources=packagerevisions/status,verbs=get

// SetupWithManager sets up the controller with the Manager.
func (r *reconciler) Setup(mgr ctrl.Manager, cfg *ctrlconfig.ControllerConfig) (map[schema.GroupVersionKind]chan event.GenericEvent, error) {
	//if err := capiv1beta1.AddToScheme(mgr.GetScheme()); err != nil {
	//	return nil, err
	//}

	r.Client = mgr.GetClient()
	r.porchClient = cfg.PorchClient

	return nil, ctrl.NewControllerManagedBy(mgr).
		Named("BootstrapController").
		For(&corev1.Secret{}).
		Complete(r)
}

type reconciler struct {
	client.Client
	porchClient client.Client

	l logr.Logger
}

func (r *reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	r.l = log.FromContext(ctx)

	cr := &corev1.Secret{}
	if err := r.Get(ctx, req.NamespacedName, cr); err != nil {
		// if the resource no longer exists the reconcile loop is done
		if resource.IgnoreNotFound(err) != nil {
			msg := "cannot get resource"
			r.l.Error(err, msg)
			return ctrl.Result{}, errors.Wrap(resource.IgnoreNotFound(err), msg)
		}
		return reconcile.Result{}, nil
	}

	// if the secret is being deleted dont do anything for now
	if cr.DeletionTimestamp != nil {
		return reconcile.Result{}, nil
	}

	// this branch handles installing the secrets to the remote cluster
	if cr.GetNamespace() == "config-management-system" {
		r.l.Info("reconcile")
		clusterName, ok := cr.GetAnnotations()["nephio.org/site"]
		if !ok {
			return reconcile.Result{}, nil
		}
		if clusterName != "mgmt" {
			secrets := &corev1.SecretList{}
			if err := r.List(ctx, secrets); err != nil {
				msg := "cannot lis secrets"
				r.l.Error(err, msg)
				return ctrl.Result{RequeueAfter: 5 * time.Second}, errors.Wrap(err, msg)
			}
			found := false
			for _, secret := range secrets.Items {
				if strings.Contains(secret.GetName(), clusterName) {
					clusterClient, ok := cluster.Cluster{Client: r.Client}.GetClusterClient(&secret)
					if ok {
						found = true
						clusterClient, ready, err := clusterClient.GetClusterClient(ctx)
						if err != nil {
							msg := "cannot get clusterClient"
							r.l.Error(err, msg)
							return ctrl.Result{RequeueAfter: 30 * time.Second}, errors.Wrap(err, msg)
						}
						if !ready {
							r.l.Info("cluster not ready")
							return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
						}
						ns := &corev1.Namespace{}
						if err = clusterClient.Get(ctx, types.NamespacedName{Name: cr.GetNamespace()}, ns); err != nil {
							if resource.IgnoreNotFound(err) != nil {
								msg := fmt.Sprintf("cannot get namespace: %s", secret.GetNamespace())
								r.l.Error(err, msg)
								return ctrl.Result{RequeueAfter: 30 * time.Second}, errors.Wrap(err, msg)
							}
							msg := fmt.Sprintf("namespace: %s, does not exist, retry...", secret.GetNamespace())
							r.l.Info(msg)
							return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
						}

						if err := clusterClient.Apply(ctx, cr); err != nil {
							msg := fmt.Sprintf("cannot apply secret to cluster %s", clusterName)
							r.l.Error(err, msg)
							return ctrl.Result{RequeueAfter: 10 * time.Second}, errors.Wrap(err, msg)
						}
					}
				}
			}
			if !found {
				return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
			}
		}
	} else {
		// this branch handles manifest installation
		clusterClient, ok := cluster.Cluster{Client: r.Client}.GetClusterClient(cr)
		if ok {
			r.l.Info("reconcile")
			clusterClient, ready, err := clusterClient.GetClusterClient(ctx)
			if err != nil {
				msg := "cannot get clusterClient"
				r.l.Error(err, msg)
				return ctrl.Result{RequeueAfter: 30 * time.Second}, errors.Wrap(err, msg)
			}
			if !ready {
				r.l.Info("cluster not ready")
				return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
			}
			pods := &corev1.PodList{}
			if err = clusterClient.List(ctx, pods); err != nil {
				msg := "cannot get Pod List"
				r.l.Error(err, msg)
				return ctrl.Result{RequeueAfter: 30 * time.Second}, errors.Wrap(err, msg)
			}

			r.l.Info("pod", "cluster", req.NamespacedName, "items", len(pods.Items))
			if len(pods.Items) == 0 {
				return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
			}
		}
	}
	return ctrl.Result{}, nil
}
