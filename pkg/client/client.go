package client

import (
	"github.com/pkg/errors"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

var clientset *kubernetes.Clientset

func Init(kubeConfig string) error {
	var (
		restconfig *rest.Config
		err        error
	)

	if len(kubeConfig) > 0 {
		restconfig, err = clientcmd.BuildConfigFromFlags("", kubeConfig)
		if err != nil {
			return errors.Wrap(err, "error in clientcmd.BuildConfigFromFlags")
		}
	} else {
		restconfig, err = rest.InClusterConfig()
		if err != nil {
			return errors.Wrap(err, "error in rest.InClusterConfig")
		}
	}

	clientset, err = kubernetes.NewForConfig(restconfig)
	if err != nil {
		return errors.Wrap(err, "error in kubernetes.NewForConfig")
	}

	return nil
}

func GetKubernetesClient() *kubernetes.Clientset {
	return clientset
}
