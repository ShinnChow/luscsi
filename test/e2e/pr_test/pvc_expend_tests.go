package E2eTest

import (
	"context"
	"fmt"
	"github.com/luskits/luscsi/test/e2e/utils"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"time"
)

var _ = ginkgo.Describe("pvc expend test ", ginkgo.Label("pr-e2e"), func() {

	config, err := clientcmd.BuildConfigFromFlags("", "/home/github-runner/.kube/config")
	gomega.ExpectWithOffset(2, err).NotTo(gomega.HaveOccurred(), "failed build config")
	kubeClient, err := kubernetes.NewForConfig(config)
	gomega.ExpectWithOffset(2, err).NotTo(gomega.HaveOccurred(), "failed new client set")

	ginkgo.Context("Configure the base environment", func() {
		ginkgo.It("check k8s status", func() {
			err := utils.K8sReadyCheck()
			gomega.Expect(err).To(gomega.BeNil())

		})
		ginkgo.It("install luscsi", func() {
			err = utils.InstallLuscsi()
			gomega.Expect(err).To(gomega.BeNil())

		})

	})

	ginkgo.Context("Checking Component Status", func() {
		ginkgo.It("waiting for luscsi ready", func() {
			logrus.Infof("waiting for luscsi ready")
			time.Sleep(120 * time.Second)
			podList, _ := kubeClient.CoreV1().Pods("luscsi").List(context.TODO(), metav1.ListOptions{})
			deploymentList, _ := kubeClient.AppsV1().Deployments("luscsi").List(context.TODO(), metav1.ListOptions{})
			for _, pod := range podList.Items {
				for {
					onePod, err := kubeClient.CoreV1().Pods("luscsi").Get(context.Background(), pod.Name, metav1.GetOptions{})
					// check pod status should be running
					onePodStatus := string(onePod.Status.Phase)
					logrus.Printf("***** wait for pod[%s] status: %s\n", onePod.Name, onePodStatus)
					gomega.ExpectWithOffset(2, err).NotTo(gomega.HaveOccurred(), "failed get job related pod")
					if onePodStatus == "Running" || onePodStatus == "Failed" || onePodStatus == "Succeeded" || onePodStatus == "Pending" {
						gomega.Expect(onePodStatus).To(gomega.Or(gomega.MatchRegexp("Running"), gomega.MatchRegexp("Succeeded")))
						logrus.Printf("* wait for pod[%s] status: %s\n", pod.Name, onePodStatus)
						break
					}
					time.Sleep(10 * time.Second)
				}
			}

			logrus.Printf("ns: luscsi deployment should be ready")
			for _, dm := range deploymentList.Items {
				fmt.Println(dm.Name, "======>", dm.Status.Replicas)
				gomega.Expect(dm.Status.ReadyReplicas).To(gomega.Equal(dm.Status.AvailableReplicas))
			}

		})

	})

	ginkgo.Context("Create test sc", func() {
		ginkgo.It("Create test sc", func() {
			VolumeBindingImmediateObj := storagev1.VolumeBindingImmediate
			deleteObj := corev1.PersistentVolumeReclaimDelete
			AllowVolumeExpansionObj := true
			sc := &storagev1.StorageClass{
				ObjectMeta: metav1.ObjectMeta{
					Name: "luscsi.luskits.io",
				},
				Provisioner:          "luscsi.luskits.io",
				VolumeBindingMode:    &VolumeBindingImmediateObj,
				AllowVolumeExpansion: &AllowVolumeExpansionObj,
				ReclaimPolicy:        &deleteObj,
				Parameters: map[string]string{
					"mgsAddress":  "10.6.113.40@tcp",
					"fsName":      "lstore",
					"sharePath":   "/csi~volume",
					"allowDelete": "true",
				},
			}
			_, err = kubeClient.StorageV1().StorageClasses().Create(context.Background(), sc, metav1.CreateOptions{})
			gomega.ExpectWithOffset(2, err).NotTo(gomega.HaveOccurred(), "failed create storage class")
		})

	})

	ginkgo.Context("Create test pvc", func() {
		ginkgo.It("Create test pvc", func() {
			storageClassName := "luscsi.luskits.io"
			pvc := &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name: "luscsi-volume",
				},
				Spec: corev1.PersistentVolumeClaimSpec{
					AccessModes: []corev1.PersistentVolumeAccessMode{
						corev1.ReadWriteMany,
					},
					StorageClassName: &storageClassName,
					Resources: corev1.VolumeResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceStorage: resource.MustParse("10Gi"),
						},
					},
				},
			}
			_, err = kubeClient.CoreV1().PersistentVolumeClaims("default").Create(context.Background(), pvc, metav1.CreateOptions{})
			gomega.ExpectWithOffset(2, err).NotTo(gomega.HaveOccurred(), "failed create pvc")
		})

	})
	ginkgo.Context("Create test pod", func() {
		ginkgo.It("Create test pod", func() {
			logrus.Infof("Create test pod")
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name: "busybox-pod-luscsi",
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "busybox-container",
							Image: "busybox:1.36",
							Args: []string{
								"/bin/sh",
								"-c",
								"sleep 3600",
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "busybox-volume",
									MountPath: "/data",
									ReadOnly:  false,
								},
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "busybox-volume",
							VolumeSource: corev1.VolumeSource{
								PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
									ClaimName: "luscsi-volume",
								},
							},
						},
					},
				},
			}
			_, err = kubeClient.CoreV1().Pods("default").Create(context.Background(), pod, metav1.CreateOptions{})
			gomega.ExpectWithOffset(2, err).NotTo(gomega.HaveOccurred(), "failed create pod")
		})

	})
	ginkgo.Context("Checking Pod Status", func() {
		ginkgo.It("Checking Pod Status", func() {
			logrus.Infof("Checking Pod Status")
			err = wait.PollUntilContextCancel(context.Background(), 10*time.Second, true, func(ctx context.Context) (bool, error) {
				pod, err := kubeClient.CoreV1().Pods("default").Get(context.Background(), "busybox-pod-luscsi", metav1.GetOptions{})
				gomega.ExpectWithOffset(2, err).NotTo(gomega.HaveOccurred(), "failed get pod")
				return pod.Status.Phase == corev1.PodRunning, nil
			})
			gomega.ExpectWithOffset(2, err).NotTo(gomega.HaveOccurred(), "failed wait pod running")
		})
	})

	ginkgo.Context("Expand pvc", func() {
		ginkgo.It("Modify spec-resources-requests-storage", func() {
			pvc, err := kubeClient.CoreV1().PersistentVolumeClaims("default").Get(context.Background(), "luscsi-volume", metav1.GetOptions{})
			gomega.ExpectWithOffset(2, err).NotTo(gomega.HaveOccurred(), "failed get pvc")
			pvc.Spec.Resources.Requests[corev1.ResourceStorage] = resource.MustParse("20Gi")
			_, err = kubeClient.CoreV1().PersistentVolumeClaims("default").Update(context.Background(), pvc, metav1.UpdateOptions{})
			gomega.ExpectWithOffset(2, err).NotTo(gomega.HaveOccurred(), "failed update pvc")
			logrus.Infof("waiting for pvc resize")
			err = wait.PollUntilContextCancel(context.Background(), 20*time.Second, true, func(ctx context.Context) (bool, error) {
				pvc, err := kubeClient.CoreV1().PersistentVolumeClaims("default").Get(context.Background(), "luscsi-volume", metav1.GetOptions{})
				gomega.ExpectWithOffset(2, err).NotTo(gomega.HaveOccurred(), "failed get pvc")
				logrus.Infof("pvc resize status: %s", pvc.Status.Capacity[corev1.ResourceStorage])
				return pvc.Status.Capacity[corev1.ResourceStorage] == resource.MustParse("20Gi"), nil
			})
			gomega.ExpectWithOffset(2, err).NotTo(gomega.HaveOccurred(), "failed wait pvc resize")
			logrus.Infof("pvc resize success")

		})

	})

})
