package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	_ "github.com/henderiw-nephio/nephio-controllers/controllers/pkg/reconcilers/bootstrap-packages"
	_ "github.com/henderiw-nephio/nephio-controllers/controllers/pkg/reconcilers/bootstrap-secret"
	ctrlrconfig "github.com/henderiw-nephio/nephio-controllers/controllers/pkg/reconcilers/config"
	_ "github.com/henderiw-nephio/nephio-controllers/controllers/pkg/reconcilers/generic-specializer"
	//_ "github.com/henderiw-nephio/nephio-controllers/controllers/pkg/reconcilers/ipam-specializer"
	_ "github.com/henderiw-nephio/nephio-controllers/controllers/pkg/reconcilers/approval"
	_ "github.com/henderiw-nephio/nephio-controllers/controllers/pkg/reconcilers/repository"
	_ "github.com/henderiw-nephio/nephio-controllers/controllers/pkg/reconcilers/token"
	//_ "github.com/henderiw-nephio/nephio-controllers/controllers/pkg/reconcilers/vlan-specializer"
	"github.com/henderiw-nephio/nephio-controllers/pkg/giteaclient"
	_ "github.com/henderiw-nephio/network/controllers/pkg/reconcilers/network"
	_ "github.com/henderiw-nephio/network/controllers/pkg/reconcilers/networkconfig"
	_ "github.com/henderiw-nephio/network/controllers/pkg/reconcilers/target"
	//_ "github.com/nephio-project/nephio/controllers/pkg/reconcilers/ipam-specializer"
	//_ "github.com/nephio-project/nephio/controllers/pkg/reconcilers/vlan-specializer"
	"github.com/henderiw-nephio/network/pkg/targets"
	porchclient "github.com/nephio-project/nephio/controllers/pkg/porch/client"
	reconcilerinterface "github.com/nephio-project/nephio/controllers/pkg/reconcilers/reconciler-interface"
	//_ "github.com/nephio-project/nephio/controllers/pkg/reconcilers/repository"
	"github.com/nephio-project/nephio/controllers/pkg/resource"
	"github.com/nokia/k8s-ipam/pkg/proxy/clientproxy"
	"github.com/nokia/k8s-ipam/pkg/proxy/clientproxy/ipam"
	"github.com/nokia/k8s-ipam/pkg/proxy/clientproxy/vlan"
	"go.uber.org/zap/zapcore"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

var (
	setupLog = ctrl.Log.WithName("setup")
)

func main() {
	var metricsAddr string
	var enableLeaderElection bool
	var probeAddr string
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	opts := zap.Options{
		Development: true,
		TimeEncoder: zapcore.ISO8601TimeEncoder,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		setupLog.Error(err, "cannot initializer schema")
		os.Exit(1)
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                     scheme,
		MetricsBindAddress:         metricsAddr,
		Port:                       9443,
		HealthProbeBindAddress:     probeAddr,
		LeaderElection:             enableLeaderElection,
		LeaderElectionID:           "nephio-operators.nephio.org",
		LeaderElectionResourceLock: resourcelock.LeasesResourceLock,
	})
	if err != nil {
		setupLog.Error(err, "cannot create manager")
		os.Exit(1)
	}

	setupLog.Info("setup controller")
	ctx := ctrl.SetupSignalHandler()
	// runs a generic client which keeps the connection open
	gc := giteaclient.New(resource.NewAPIPatchingApplicator(mgr.GetClient()))
	go gc.Start(ctx)

	porchClient, err := porchclient.CreateClient(ctrl.GetConfigOrDie())
	if err != nil {
		setupLog.Error(err, "unable to create porch client:")
		os.Exit(1)
	}

	backendAddress := fmt.Sprintf("%s.%s.svc.cluster.local:%s", "resource-backend-controller-grpc-svc", "backend-system", "9999")
	ctrlCfg := &ctrlrconfig.ControllerConfig{
		Address:     backendAddress,
		GiteaClient: gc,
		PorchClient: porchClient,
		IpamClientProxy: ipam.New(ctx, clientproxy.Config{
			Address: backendAddress,
		}),
		VlanClientProxy: vlan.New(ctx, clientproxy.Config{
			Address: backendAddress,
		}),
		Targets: targets.New(),
	}

	for name, reconciler := range reconcilerinterface.Reconcilers {
		setupLog.Info("reconciler", "name", name, "enabled", IsReconcilerEnabled(name))
		if IsReconcilerEnabled(name) {
			if _, err := reconciler.SetupWithManager(ctx, mgr, ctrlCfg); err != nil {
				setupLog.Error(err, "unable to set up health check")
				os.Exit(1)
			}

		}
	}
	//+kubebuilder:scaffold:builder

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctx); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}

func IsReconcilerEnabled(reconcilerName string) bool {
	if _, found := os.LookupEnv(fmt.Sprintf("ENABLE_%s", strings.ToUpper(reconcilerName))); found {
		return true
	}
	return false
}
