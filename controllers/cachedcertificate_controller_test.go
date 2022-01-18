/*
Copyright 2021.

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

package controllers

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	cachev1alpha1 "weavelab.xyz/cached-certificate-operator/api/v1alpha1"
)

var _ = Describe("The CachedCertificate controller", func() {
	// Define utility constants for object names and testing timeouts/durations and intervals.
	const (
		timeout  = time.Second * 10
		interval = time.Millisecond * 250
	)

	ctx := context.Background()

	When("syncing a CachedCertificate", func() {
		var upstreamSecret *v1.Secret
		var downstreamSecretLookupKey types.NamespacedName

		It("should create a new upstream certificate and sync the resulting secret", func() {
			const (
				CachedCertificateName      = "new-cachedcertificate"
				CachedCertificateNamespace = "testing"
			)

			cachedCert := &cachev1alpha1.CachedCertificate{
				ObjectMeta: metav1.ObjectMeta{
					Name:      CachedCertificateName,
					Namespace: CachedCertificateNamespace,
				},
				Spec: cachev1alpha1.CachedCertificateSpec{
					IssuerRef: cachev1alpha1.IssuerRef{
						Name: "my-issuer",
						Kind: "Issuer",
					},
					DNSNames: []string{
						"example.com",
					},
				},
			}
			Expect(k8sClient.Create(ctx, cachedCert)).Should(Succeed())

			upstreamCertName := getUpstreamCertificateName(cachedCert.Spec.DNSNames...)
			By("creating the upstream Certificate", func() {
				upstreamCertLookupKey := types.NamespacedName{Name: upstreamCertName, Namespace: "testing"}
				upstreamCert := &unstructured.Unstructured{}
				upstreamCert.SetGroupVersionKind(schema.GroupVersionKind{
					Group:   "cert-manager.io",
					Kind:    "Certificate",
					Version: "v1",
				})

				Eventually(func() error {
					return k8sClient.Get(ctx, upstreamCertLookupKey, upstreamCert)
				}, timeout, interval).Should(Succeed())
			})

			// Manually create the secret that would normally be provisioned by cert-manager
			// save it for later use to test change syncs
			upstreamSecret = &v1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      upstreamCertName,
					Namespace: "testing",
					Annotations: map[string]string{
						// Cert-manager creates this annotation and we depend on it for fast
						// reconciliation of secrets as they change
						CertificateNameAnnotationKey: upstreamCertName,
					},
				},
				Data: map[string][]byte{
					"tls.crt": nil,
					"tls.key": nil,
				},
			}
			Expect(k8sClient.Create(ctx, upstreamSecret)).Should(Succeed())

			By("ensuring the downstream secret is created", func() {
				downstreamSecretLookupKey = types.NamespacedName{Name: CachedCertificateName, Namespace: CachedCertificateNamespace}
				syncedSecret := &v1.Secret{}
				Eventually(func() error {
					return k8sClient.Get(ctx, downstreamSecretLookupKey, syncedSecret)
				}, timeout, interval).Should(Succeed())
			})

			By("ensuring final status on the CachedCertificate", func() {
				cachedCertLookupKey := types.NamespacedName{Name: CachedCertificateName, Namespace: CachedCertificateNamespace}
				createdCachedCert := &cachev1alpha1.CachedCertificate{}

				Eventually(func() interface{} {
					_ = k8sClient.Get(ctx, cachedCertLookupKey, createdCachedCert)
					return createdCachedCert.Status.State
				}, timeout, interval).Should(Equal(cachev1alpha1.CachedCertificateStateSynced))

				Expect(createdCachedCert.Status).To(Equal(
					cachev1alpha1.CachedCertificateStatus{
						UpstreamReady: true,
						UpstreamRef: &cachev1alpha1.ObjectReference{
							Name:      upstreamCertName,
							Namespace: "testing",
						},
						State: cachev1alpha1.CachedCertificateStateSynced,
					},
				))
			})
		})

		It("must watch the upstream secret for changes and sync", func() {
			newData := map[string][]byte{
				"tls.crt": []byte("cert changed"),
				"tls.key": []byte("key changed"),
				"new":     []byte("added data"),
			}
			upstreamSecret.Data = newData
			Expect(k8sClient.Update(ctx, upstreamSecret)).Should(Succeed())

			Eventually(func() (map[string][]byte, error) {
				downstreamSecret := &v1.Secret{}
				err := k8sClient.Get(ctx, downstreamSecretLookupKey, downstreamSecret)
				if err != nil {
					return nil, err
				}
				return downstreamSecret.Data, nil
			}, timeout, interval).Should(Equal(newData))
		})
	})

	When("syncing a synced CachedCertificate", func() {
		var upstreamSecret *v1.Secret

		It("should handle dnsNames changes", func() {
			const (
				CachedCertificateName      = "cachedcertificate-dnsnameschange"
				CachedCertificateNamespace = "testing"
			)

			cachedCert := &cachev1alpha1.CachedCertificate{
				ObjectMeta: metav1.ObjectMeta{
					Name:      CachedCertificateName,
					Namespace: CachedCertificateNamespace,
				},
				Spec: cachev1alpha1.CachedCertificateSpec{
					IssuerRef: cachev1alpha1.IssuerRef{
						Name: "my-issuer",
						Kind: "Issuer",
					},
					DNSNames: []string{
						"dnsset-1.example.com",
					},
				},
			}
			Expect(k8sClient.Create(ctx, cachedCert)).Should(Succeed())

			upstreamCertName := getUpstreamCertificateName(cachedCert.Spec.DNSNames...)
			By("creating the upstream Certificate", func() {
				upstreamCertLookupKey := types.NamespacedName{Name: upstreamCertName, Namespace: "testing"}
				upstreamCert := &unstructured.Unstructured{}
				upstreamCert.SetGroupVersionKind(schema.GroupVersionKind{
					Group:   "cert-manager.io",
					Kind:    "Certificate",
					Version: "v1",
				})

				Eventually(func() error {
					return k8sClient.Get(ctx, upstreamCertLookupKey, upstreamCert)
				}, timeout, interval).Should(Succeed())
			})

			// Manually create the secret that would normally be provisioned by cert-manager
			// save it for later use to test change syncs
			upstreamSecret = &v1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      upstreamCertName,
					Namespace: "testing",
					Annotations: map[string]string{
						// Cert-manager creates this annotation and we depend on it for fast
						// reconciliation of secrets as they change
						CertificateNameAnnotationKey: upstreamCertName,
					},
				},
				Data: map[string][]byte{
					"tls.crt": nil,
					"tls.key": nil,
				},
			}
			Expect(k8sClient.Create(ctx, upstreamSecret)).Should(Succeed())

			By("ensuring final status on the CachedCertificate", func() {
				cachedCertLookupKey := types.NamespacedName{Name: CachedCertificateName, Namespace: CachedCertificateNamespace}
				createdCachedCert := &cachev1alpha1.CachedCertificate{}

				Eventually(func() interface{} {
					_ = k8sClient.Get(ctx, cachedCertLookupKey, createdCachedCert)
					return createdCachedCert.Status.State
				}, timeout, interval).Should(Equal(cachev1alpha1.CachedCertificateStateSynced))

				Expect(createdCachedCert.Status).To(Equal(
					cachev1alpha1.CachedCertificateStatus{
						UpstreamReady: true,
						UpstreamRef: &cachev1alpha1.ObjectReference{
							Name:      upstreamCertName,
							Namespace: "testing",
						},
						State: cachev1alpha1.CachedCertificateStateSynced,
					},
				))

				// Update the DNS names
				createdCachedCert.Spec.DNSNames[0] = "dnsset-2.example.com"
				Expect(k8sClient.Update(ctx, createdCachedCert)).Should(Succeed())

				// store new cert name
				newUpstreamCertName := getUpstreamCertificateName(createdCachedCert.Spec.DNSNames...)

				// Manually create the secret that would normally be provisioned by cert-manager
				newUpstreamSecret := &v1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      newUpstreamCertName,
						Namespace: "testing",
						Annotations: map[string]string{
							// Cert-manager creates this annotation and we depend on it for fast
							// reconciliation of secrets as they change
							CertificateNameAnnotationKey: newUpstreamCertName,
						},
					},
					Data: map[string][]byte{
						"tls.crt": nil,
						"tls.key": nil,
					},
				}
				Expect(k8sClient.Create(ctx, newUpstreamSecret)).Should(Succeed())

				// wait for the ref to change
				Eventually(func() interface{} {
					_ = k8sClient.Get(ctx, cachedCertLookupKey, createdCachedCert)
					return createdCachedCert.Status
				}, timeout, interval).Should(Equal(
					cachev1alpha1.CachedCertificateStatus{
						UpstreamReady: true,
						UpstreamRef: &cachev1alpha1.ObjectReference{
							Name:      newUpstreamCertName,
							Namespace: "testing",
						},
						State: cachev1alpha1.CachedCertificateStateSynced,
					},
				))

				// Update the DNS names to an existing upstream (back to the original in this case)
				Eventually(func() interface{} {
					_ = k8sClient.Get(ctx, cachedCertLookupKey, createdCachedCert)
					return createdCachedCert.Status.State
				}, timeout, interval).Should(Equal(cachev1alpha1.CachedCertificateStateSynced))

				createdCachedCert.Spec.DNSNames[0] = "dnsset-1.example.com"
				Expect(k8sClient.Update(ctx, createdCachedCert)).Should(Succeed())

				// wait for the ref to change
				revertedUpstreamCertName := getUpstreamCertificateName(createdCachedCert.Spec.DNSNames...)
				Eventually(func() interface{} {
					_ = k8sClient.Get(ctx, cachedCertLookupKey, createdCachedCert)
					return createdCachedCert.Status
				}, timeout, interval).Should(Equal(
					cachev1alpha1.CachedCertificateStatus{
						UpstreamReady: true,
						UpstreamRef: &cachev1alpha1.ObjectReference{
							Name:      revertedUpstreamCertName,
							Namespace: "testing",
						},
						State: cachev1alpha1.CachedCertificateStateSynced,
					},
				))
			})
		})
	})

	When("syncing a CachedCertificate", func() {
		It("should refuse to update a secret it did not make", func() {
			const (
				CachedCertificateName      = "new-cachedcertificate-conflict"
				CachedCertificateNamespace = "testing"
			)

			cachedCert := &cachev1alpha1.CachedCertificate{
				ObjectMeta: metav1.ObjectMeta{
					Name:      CachedCertificateName,
					Namespace: CachedCertificateNamespace,
				},
				Spec: cachev1alpha1.CachedCertificateSpec{
					IssuerRef: cachev1alpha1.IssuerRef{
						Name: "my-issuer",
						Kind: "Issuer",
					},
					DNSNames: []string{
						"conflict.example.com",
					},
				},
			}
			Expect(k8sClient.Create(ctx, cachedCert)).Should(Succeed())

			upstreamCertName := getUpstreamCertificateName(cachedCert.Spec.DNSNames...)
			By("creating an upstream cert", func() {
				upstreamCertLookupKey := types.NamespacedName{Name: upstreamCertName, Namespace: "testing"}
				upstreamCert := &unstructured.Unstructured{}
				upstreamCert.SetGroupVersionKind(schema.GroupVersionKind{
					Group:   "cert-manager.io",
					Kind:    "Certificate",
					Version: "v1",
				})

				// We'll need to retry getting the upstreamCert
				Eventually(func() error {
					return k8sClient.Get(ctx, upstreamCertLookupKey, upstreamCert)
				}, timeout, interval).Should(Succeed())
			})

			// Manually create the secret that would normally be provisioned by cert-manager
			upstreamSecret := &v1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      upstreamCertName,
					Namespace: "testing",
				},
				Data: map[string][]byte{
					"tls.crt": nil,
					"tls.key": nil,
				},
			}
			Expect(k8sClient.Create(ctx, upstreamSecret)).Should(Succeed())

			// assert that the synced succeeded
			cachedCertLookupKey := types.NamespacedName{Name: CachedCertificateName, Namespace: CachedCertificateNamespace}
			createdCachedCert := &cachev1alpha1.CachedCertificate{}

			By("ensuring sucess status on the CachedCertificate", func() {
				Eventually(func() interface{} {
					_ = k8sClient.Get(ctx, cachedCertLookupKey, createdCachedCert)
					return createdCachedCert.Status.State
				}, timeout, interval).Should(Equal(cachev1alpha1.CachedCertificateStateSynced))
			})

			// Fatch and update the secret to take away the labels
			// this will trigger a resync -- which should then fail
			syncedSecret := &v1.Secret{}
			Expect(
				k8sClient.Get(ctx, types.NamespacedName{Name: CachedCertificateName, Namespace: CachedCertificateNamespace}, syncedSecret),
			).Should(Succeed())
			syncedSecret.Labels = make(map[string]string)
			Expect(k8sClient.Update(ctx, syncedSecret)).Should(Succeed())

			// the sync should get marked as error
			Eventually(func() interface{} {
				_ = k8sClient.Get(ctx, cachedCertLookupKey, createdCachedCert)
				return createdCachedCert.Status.State
			}, timeout, interval).Should(Equal(cachev1alpha1.CachedCertificateStateError))
		})
	})

	When("syncing a CachedCertificate", func() {
		It("should report an error when the upstream has no secretName", func() {
			const (
				CachedCertificateName      = "new-cachedcertificate-bad-upstream-cert"
				CachedCertificateNamespace = "testing"
			)

			ctx := context.Background()

			cachedCert := &cachev1alpha1.CachedCertificate{
				ObjectMeta: metav1.ObjectMeta{
					Name:      CachedCertificateName,
					Namespace: CachedCertificateNamespace,
				},
				Spec: cachev1alpha1.CachedCertificateSpec{
					IssuerRef: cachev1alpha1.IssuerRef{
						Name: "my-issuer",
						Kind: "Issuer",
					},
					DNSNames: []string{
						"bad.example.com",
					},
				},
			}
			Expect(k8sClient.Create(ctx, cachedCert)).Should(Succeed())

			upstreamCertName := getUpstreamCertificateName(cachedCert.Spec.DNSNames...)
			By("creating an upstream cert", func() {
				upstreamCertLookupKey := types.NamespacedName{Name: upstreamCertName, Namespace: "testing"}
				upstreamCert := &unstructured.Unstructured{}
				upstreamCert.SetGroupVersionKind(schema.GroupVersionKind{
					Group:   "cert-manager.io",
					Kind:    "Certificate",
					Version: "v1",
				})

				// We'll need to retry getting the upstreamCert
				Eventually(func() error {
					return k8sClient.Get(ctx, upstreamCertLookupKey, upstreamCert)
				}, timeout, interval).Should(Succeed())

				// Now "break" it by removing the spec then update
				_ = unstructured.SetNestedField(upstreamCert.Object, "", "spec", "secretName")

				Eventually(func() error {
					return k8sClient.Update(ctx, upstreamCert)
				}, timeout, interval).Should(Succeed())
			})

			cachedCertLookupKey := types.NamespacedName{Name: CachedCertificateName, Namespace: CachedCertificateNamespace}
			createdCachedCert := &cachev1alpha1.CachedCertificate{}

			// patch the cachedCert to trigger a resync
			Expect(k8sClient.Get(ctx, cachedCertLookupKey, cachedCert)).Should(Succeed())
			cachedCert.Annotations = map[string]string{"update": "now"}
			Expect(k8sClient.Update(ctx, cachedCert)).Should(Succeed())

			By("ensuring error status on the CachedCertificate", func() {
				Eventually(func() interface{} {
					_ = k8sClient.Get(ctx, cachedCertLookupKey, createdCachedCert)
					return createdCachedCert.Status.State
				}, timeout, interval).Should(Equal(cachev1alpha1.CachedCertificateStateError))
			})
		})
	})

	When("syncing a missing CachedCertificate", func() {
		It("should exit without requeue or err", func() {
			Expect(reconciler.Reconcile(ctx, controllerruntime.Request{
				NamespacedName: types.NamespacedName{
					Namespace: "testing",
					Name:      "does-not-exist",
				},
			})).To(Equal(reconcile.Result{}))
		})
	})
})
