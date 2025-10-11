package utils

import (
	"context"
	"strings"

	kruisePub "github.com/openkruise/kruise-api/apps/pub"
	gamekruiseiov1alpha1 "github.com/openkruise/kruise-game/apis/v1alpha1"
	cperrors "github.com/openkruise/kruise-game/cloudprovider/errors"
	"github.com/openkruise/kruise-game/pkg/util"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	log "k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func AllowNotReadyContainers(c client.Client, ctx context.Context, pod *corev1.Pod, svc *corev1.Service, isSvcShared bool) (bool, cperrors.PluginError) {
	// get lifecycleState
	lifecycleState, exist := pod.GetLabels()[kruisePub.LifecycleStateKey]
	log.Infof("lifecycleState为%s", lifecycleState)
	// get gss
	gss, err := util.GetGameServerSetOfPod(pod, c, ctx)
	if err != nil {
		return false, cperrors.ToPluginError(err, cperrors.ApiCallError)
	}

	// get allowNotReadyContainers
	var allowNotReadyContainers []string
	for _, kv := range gss.Spec.Network.NetworkConf {
		if kv.Name == gamekruiseiov1alpha1.AllowNotReadyContainersNetworkConfName {
			for _, allowNotReadyContainer := range strings.Split(kv.Value, ",") {
				if allowNotReadyContainer != "" {
					allowNotReadyContainers = append(allowNotReadyContainers, allowNotReadyContainer)
				}
			}
		}
	}
	log.Infof("allowNotReadyContainers为%v", allowNotReadyContainers)
	// PreInplaceUpdating
	if exist && lifecycleState == string(kruisePub.LifecycleStatePreparingUpdate) {
		// ensure PublishNotReadyAddresses is true when containers pre-updating
		if !svc.Spec.PublishNotReadyAddresses && util.IsContainersPreInplaceUpdating(pod, gss, allowNotReadyContainers) {
			log.Info("设置PublishNotReadyAddresses为true")
			svc.Spec.PublishNotReadyAddresses = true
			return true, nil
		}

		// ensure remove finalizer
		if svc.Spec.PublishNotReadyAddresses || !util.IsContainersPreInplaceUpdating(pod, gss, allowNotReadyContainers) {
			log.Info("设置game.kruise.io/inplace-update-not-ready-blocker为false")
			pod.GetLabels()[gamekruiseiov1alpha1.InplaceUpdateNotReadyBlocker] = "false"
		}
	} else {
		log.Info("lifecycleState不为PreparingUpdate状态")
		log.Info("设置game.kruise.io/inplace-update-not-ready-blocker为true")
		pod.GetLabels()[gamekruiseiov1alpha1.InplaceUpdateNotReadyBlocker] = "true"
		if !svc.Spec.PublishNotReadyAddresses {
			return false, nil
		}
		if isSvcShared {
			// ensure PublishNotReadyAddresses is false when all pods are updated
			if gss.Status.UpdatedReplicas == gss.Status.Replicas {
				podList := &corev1.PodList{}
				err := c.List(ctx, podList, &client.ListOptions{
					Namespace: gss.GetNamespace(),
					LabelSelector: labels.SelectorFromSet(map[string]string{
						gamekruiseiov1alpha1.GameServerOwnerGssKey: gss.GetName(),
					})})
				if err != nil {
					return false, cperrors.ToPluginError(err, cperrors.ApiCallError)
				}
				for _, p := range podList.Items {
					_, condition := util.GetPodConditionFromList(p.Status.Conditions, corev1.PodReady)
					if condition == nil || condition.Status != corev1.ConditionTrue {
						return false, nil
					}
				}
				svc.Spec.PublishNotReadyAddresses = false
				return true, nil
			}
		} else {
			_, condition := util.GetPodConditionFromList(pod.Status.Conditions, corev1.PodReady)
			if condition == nil || condition.Status != corev1.ConditionTrue {
				log.Info("pod未就绪")
				return false, nil
			}
			log.Info("pod已就绪，设置PublishNotReadyAddresses为false")
			svc.Spec.PublishNotReadyAddresses = false
			return true, nil
		}
	}
	return false, nil
}
