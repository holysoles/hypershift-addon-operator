package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	hyperv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

func TestLabelAgentStartsWithoutLocalManagedClusterCRD(t *testing.T) {
	mcCRDDir := t.TempDir()
	hostedClusterCRD, err := os.ReadFile(filepath.Join("..", "..", "hack", "crds", "hypershift.openshift.io_hostedclusters.yaml"))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(mcCRDDir, "hostedclusters.yaml"), hostedClusterCRD, 0o600))

	mcEnv := &envtest.Environment{CRDDirectoryPaths: []string{mcCRDDir}}
	mcConfig, err := mcEnv.Start()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, mcEnv.Stop()) })

	hubEnv := &envtest.Environment{CRDDirectoryPaths: []string{filepath.Join("..", "..", "hack", "crds")}}
	hubConfig, err := hubEnv.Start()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, hubEnv.Stop()) })

	scheme := runtime.NewScheme()
	require.NoError(t, hyperv1beta1.AddToScheme(scheme))
	require.NoError(t, clusterv1.AddToScheme(scheme))

	hubCache, err := cache.New(hubConfig, cache.Options{Scheme: scheme})
	require.NoError(t, err)

	shutdownTimeout := time.Second
	mgr, err := ctrl.NewManager(mcConfig, ctrl.Options{
		Scheme:                  scheme,
		HealthProbeBindAddress:  "0",
		Metrics:                 metricsserver.Options{BindAddress: "0"},
		GracefulShutdownTimeout: &shutdownTimeout,
		Controller: config.Controller{
			CacheSyncTimeout: time.Second,
		},
	})
	require.NoError(t, err)

	agent := &LabelAgent{hubCache: hubCache}
	require.NoError(t, agent.SetupWithManager(mgr))

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = hubCache.Start(ctx) }()
	require.Eventually(t, func() bool { return hubCache.WaitForCacheSync(ctx) }, 5*time.Second, 10*time.Millisecond)

	managerDone := make(chan error, 1)
	go func() { managerDone <- mgr.Start(ctx) }()

	select {
	case err := <-managerDone:
		t.Fatalf("manager stopped while the local cluster lacks the ManagedCluster CRD: %v", err)
	case <-time.After(2 * time.Second):
	}
}
