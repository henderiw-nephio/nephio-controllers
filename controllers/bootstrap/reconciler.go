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
	"time"

	"github.com/go-logr/logr"
	"github.com/henderiw-nephio/bootstrap-controller/pkg/applicator"
	"github.com/henderiw-nephio/nephio-controllers/controllers"
	ctrlconfig "github.com/henderiw-nephio/nephio-controllers/controllers/config"
	"github.com/nephio-project/nephio/controllers/pkg/resource"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	capiv1beta1 "sigs.k8s.io/cluster-api/api/v1beta1"
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
	if err := capiv1beta1.AddToScheme(mgr.GetScheme()); err != nil {
		return nil, err
	}

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
			r.l.Error(err, "cannot get resource")
			return ctrl.Result{}, errors.Wrap(resource.IgnoreNotFound(err), "cannot get resource")
		}
		return reconcile.Result{}, nil
	}

	if secret.DeletionTimestamp != nil {
		return reconcile.Result{}, nil
	}

	clusterType := getClusterType(secret)
	if clusterType != ClusterTypeNoKubeConfig {
		var err error
		var clusterClient applicator.APIPatchingApplicator
		switch clusterType {
		case ClusterTypeCapi:
			if !r.isCapiClusterReady(ctx, secret) {
				r.l.Info("cluster not ready")
				return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
			}
			// err is handled generically for all cluster types
			clusterClient, err = getCapiClusterClient(secret)
		}
		if err != nil {
			msg := fmt.Sprintf("cannot get client clusterType: %s", clusterType)
			r.l.Error(err, msg)
			return ctrl.Result{RequeueAfter: 30 * time.Second}, errors.Wrap(err, msg)
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
	return ctrl.Result{}, nil

}
