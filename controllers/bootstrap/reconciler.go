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

	secret := &corev1.Secret{}
	if err := r.Get(ctx, req.NamespacedName, secret); err != nil {
		// if the resource no longer exists the reconcile loop is done
		if resource.IgnoreNotFound(err) != nil {
			msg := "cannot get resource"
			r.l.Error(err, msg)
			return ctrl.Result{}, errors.Wrap(resource.IgnoreNotFound(err), msg)
		}
		return reconcile.Result{}, nil
	}

	// if the secret is being deleted dont do anything for now
	if secret.DeletionTimestamp != nil {
		return reconcile.Result{}, nil
	}

	if secret.GetNamespace() == "config-management-system" {
		clusterName, ok := secret.GetAnnotations()["nephio.org/site"]
		if !ok {
			return reconcile.Result{}, nil
		}
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
					namspaces := &corev1.NamespaceList{}
					if err = clusterClient.List(ctx, namspaces); err != nil {
						msg := "cannot get namspaces List"
						r.l.Error(err, msg)
						return ctrl.Result{RequeueAfter: 30 * time.Second}, errors.Wrap(err, msg)
					}

					r.l.Info("namspaces", "cluster", req.NamespacedName, "items", len(namspaces.Items))
					if len(namspaces.Items) == 0 {
						return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
					}
				}
			}
		}
		if !found {
			return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
		}
	} else {
		clusterClient, ok := cluster.Cluster{Client: r.Client}.GetClusterClient(secret)
		if ok {
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
