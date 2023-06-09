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

package giteaclient

import (
	"context"
	"fmt"
	"os"
	"time"

	"code.gitea.io/sdk/gitea"
	"github.com/go-logr/logr"
	"github.com/nephio-project/nephio/controllers/pkg/resource"
	//"github.com/nokia/k8s-ipam/pkg/resource"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type GiteaClient interface {
	Start(ctx context.Context)

	Get() *gitea.Client
}

func New(client resource.APIPatchingApplicator) GiteaClient {
	return &gc{
		client: client,
	}
}

type gc struct {
	client resource.APIPatchingApplicator

	giteaClient *gitea.Client
	l           logr.Logger
}

func (r *gc) Start(ctx context.Context) {
	r.l = log.FromContext(ctx)
	//var err error
	for {
	LOOP:
		time.Sleep(5 * time.Second)

		gitURL, ok := os.LookupEnv("GIT_URL")
		if !ok {
			r.l.Error(fmt.Errorf("git url not defined"), "cannot get secret")
			goto LOOP
		}

		namespace := os.Getenv("POD_NAMESPACE")
		if gitNamesapce, ok := os.LookupEnv("GIT_NAMESPACE"); ok {
			namespace = gitNamesapce
		}
		secretName := "git-user-secret"
		if gitSecretName, ok := os.LookupEnv("GIT_SECRET_NAME"); ok {
			namespace = gitSecretName
		}

		// get secret that was created when installing gitea
		secret := &corev1.Secret{}
		if err := r.client.Get(ctx, types.NamespacedName{
			Namespace: namespace,
			Name:      secretName,
		},
			secret); err != nil {
			r.l.Error(err, "cannot get secret")
			goto LOOP
		}

		// To create/list tokens we can only use basic authentication using username and password
		giteaClient, err := gitea.NewClient(
			gitURL,
			getClientAuth(secret))
		if err != nil {
			r.l.Error(err, "cannot authenticate to gitea")
			goto LOOP
		}

		r.giteaClient = giteaClient
		r.l.Info("gitea init done")
		return
	}
}

func getClientAuth(secret *corev1.Secret) gitea.ClientOption {
	return gitea.SetBasicAuth(string(secret.Data["username"]), string(secret.Data["password"]))
}

func (r *gc) Get() *gitea.Client {
	return r.giteaClient
}
