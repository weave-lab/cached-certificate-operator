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
	"errors"
	"time"

	"github.com/go-logr/logr"
	v1 "k8s.io/api/core/v1"
	k8serr "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	cachev1alpha1 "weavelab.xyz/cached-certificate-operator/api/v1alpha1"
)

var (
	// SyncedLabelKey is the label key used to easily find secrets created from this controller
	SyncedLabelKey = cachev1alpha1.GroupVersion.Group + "/synced-from-cache"

	// SourceAnnotationKey holds the namespace and name that matches the original source of the secret
	SourceAnnotationKey = cachev1alpha1.GroupVersion.Group + "/source"
)

// CachedCertificateReconciler reconciles a CachedCertificate object
type CachedCertificateReconciler struct {
	CacheNamespace string

	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=cache.weavelab.xyz,resources=cachedcertificates,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=cache.weavelab.xyz,resources=cachedcertificates/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=cache.weavelab.xyz,resources=cachedcertificates/finalizers,verbs=update

//+kubebuilder:rbac:groups=cert-manager.io,resources=certificates,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *CachedCertificateReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	reqLog := log.FromContext(ctx)

	cachedCert := &cachev1alpha1.CachedCertificate{}
	err := r.Get(ctx, req.NamespacedName, cachedCert)
	switch {
	case k8serr.IsNotFound(err):
		// nothing to do so exit with requeue and no err
		return ctrl.Result{}, nil
	case err != nil:
		return ctrl.Result{}, err
	}

	// default secretName to match the resource name
	if cachedCert.Spec.SecretName == "" {
		cachedCert.Spec.SecretName = cachedCert.GetName()
	}

	if cachedCert.Status.UpstreamRef == nil {
		// speculatively set the upstream if it's not already set
		cachedCert.Status.UpstreamRef = &cachev1alpha1.ObjectReference{
			Name:      getUpstreamCertificateName(cachedCert.Spec.DNSNames...),
			Namespace: r.CacheNamespace,
		}
	}

	// try to get the upstream cert
	upstreamCert, err := r.getUpstreamCertificate(ctx, cachedCert)
	if k8serr.IsNotFound(err) {
		// create if not found
		err = r.createUpstreamCertificate(ctx, cachedCert)
		if err != nil {
			return ctrl.Result{}, err
		}

		// after upstream create, set the update the status and requeue the resource
		err = r.Status().Update(ctx, cachedCert)
		if err != nil {
			return ctrl.Result{}, err
		}

		return ctrl.Result{Requeue: true}, nil
	} else if err != nil {
		reqLog.Error(err, "unexpected error getting upstream Certificate")
		return ctrl.Result{}, err
	}

	upstreamDNSNames, found, err := unstructured.NestedStringSlice(upstreamCert.Object, "spec", "dnsNames")
	if err != nil {
		reqLog.Error(err, "upstream Certificate has bad dnsNames")
		return ctrl.Result{}, err
	}

	if !found {
		reqLog.Error(err, "upstream Certificate has no dnsNames")
		return ctrl.Result{}, err
	}

	if !slicesEqualAfterSort(upstreamDNSNames, cachedCert.Spec.DNSNames) {
		// set and go back through the system to issue / re-use as needed
		cachedCert.Status.State = cachev1alpha1.CachedCertificateStatePending
		cachedCert.Status.UpstreamReady = false
		cachedCert.Status.UpstreamRef = nil

		err = r.Status().Update(ctx, cachedCert)
		if err != nil {
			return ctrl.Result{RequeueAfter: time.Second * 2}, err
		}

		return ctrl.Result{}, nil
	}

	// TODO handle Changes in the cachedcert spec?
	// TODO handle DIFFS in the CachedCertificate spec between CachedCertificates

	// try to get the secret used from which we will sync
	upstreamSecret, err := r.getUpstreamSecret(ctx, reqLog, upstreamCert)
	if k8serr.IsNotFound(err) {
		// update status if required
		if cachedCert.Status.State != cachev1alpha1.CachedCertificateStatePending || cachedCert.Status.UpstreamReady {
			cachedCert.Status.State = cachev1alpha1.CachedCertificateStatePending
			cachedCert.Status.UpstreamReady = false
			err = r.Status().Update(ctx, cachedCert)
			if err != nil {
				return ctrl.Result{}, err
			}
		}

		// requeue and wait for secret to be created
		// TODO: exponential backoff
		return ctrl.Result{Requeue: true, RequeueAfter: time.Second * 2}, nil
	} else if err != nil {
		cachedCert.Status.State = cachev1alpha1.CachedCertificateStateError
		cachedCert.Status.UpstreamReady = false
		if statusErr := r.Status().Update(ctx, cachedCert); statusErr != nil {
			reqLog.Error(err, "unable to update status on CachedCertificate")
			return ctrl.Result{}, statusErr
		}
		return ctrl.Result{}, err
	}

	// secret found, upstream is "ready"
	// update status if required
	if !cachedCert.Status.UpstreamReady {
		cachedCert.Status.UpstreamReady = true
		err = r.Status().Update(ctx, cachedCert)
		if err != nil {
			return ctrl.Result{}, err
		}
	}

	// get and validate upstream secret
	secret, err := genSecretForSync(cachedCert, upstreamCert, upstreamSecret)
	if err != nil {
		return ctrl.Result{RequeueAfter: time.Second * 3}, err
	}

	if err = validateSecret(secret); err != nil {
		return ctrl.Result{RequeueAfter: time.Second * 3}, err
	}

	err = r.upsertTargetSecret(ctx, reqLog, secret)
	if err != nil {
		cachedCert.Status.State = cachev1alpha1.CachedCertificateStateError
		err = r.Status().Update(ctx, cachedCert)
		if err != nil {
			return ctrl.Result{}, err
		}

		return ctrl.Result{}, err
	}

	// set status on cachedcertificate resource
	cachedCert.Status.State = cachev1alpha1.CachedCertificateStateSynced
	err = r.Status().Update(ctx, cachedCert)
	if err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *CachedCertificateReconciler) upsertTargetSecret(ctx context.Context, reqLog logr.Logger, secret *v1.Secret) error {
	existingSecret := &v1.Secret{}
	err := r.Get(ctx, types.NamespacedName{Name: secret.Name, Namespace: secret.Namespace}, existingSecret)
	if k8serr.IsNotFound(err) {
		return r.Create(ctx, secret)
	} else if err != nil {
		reqLog.Error(err, "unexpected error getting target Secret for sync")
		return err
	}

	// refuse to update a secret we didn't make
	if _, ok := existingSecret.GetLabels()[SyncedLabelKey]; !ok {
		return errors.New("refusing to update a secret not created by the controller")
	}

	return r.Update(ctx, secret)
}

func (r *CachedCertificateReconciler) getUpstreamCertificate(ctx context.Context, cachedCert *cachev1alpha1.CachedCertificate) (*unstructured.Unstructured, error) {
	if cachedCert.Status.UpstreamRef == nil {
		return nil, errors.New(".Status.UpstreamRef is required")
	}

	var upstreamCert unstructured.Unstructured
	upstreamCert.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "cert-manager.io",
		Kind:    "Certificate",
		Version: "v1",
	})

	err := r.Get(ctx, types.NamespacedName{
		Name:      cachedCert.Status.UpstreamRef.Name,
		Namespace: cachedCert.Status.UpstreamRef.Namespace,
	}, &upstreamCert)
	if err != nil {
		return nil, err
	}

	return &upstreamCert, nil
}

func (r *CachedCertificateReconciler) createUpstreamCertificate(ctx context.Context, cachedCert *cachev1alpha1.CachedCertificate) error {
	if cachedCert.Status.UpstreamRef == nil {
		return errors.New(".Status.UpstreamRef is required")
	}

	upstreamCert := unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "cert-manager.io/v1",
			"kind":       "Certificate",
			"metadata": map[string]interface{}{
				"name":      cachedCert.Status.UpstreamRef.Name,
				"namespace": cachedCert.Status.UpstreamRef.Namespace,

				// we intentially *do not* set ownerReferences and do not do *any* automated removal of the "Certificates" made here
			},
			"spec": map[string]interface{}{
				"dnsNames":  cachedCert.Spec.DNSNames,
				"issuerRef": cachedCert.Spec.IssuerRef,

				// The secretName of the cachedCert is for the *target* secret
				// Upstreams use their own name for secret names to ensure uniqueness in the cache namespace
				"secretName": cachedCert.Status.UpstreamRef.Name,
			},
		},
	}

	return r.Create(ctx, &upstreamCert)
}

func (r *CachedCertificateReconciler) getUpstreamSecret(ctx context.Context, reqLog logr.Logger, upstreamCert *unstructured.Unstructured) (*v1.Secret, error) {
	secretName, found, err := unstructured.NestedString(upstreamCert.Object, "spec", "secretName")
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, errors.New("unable to find secretName in upstream Certificate")
	}
	if secretName == "" {
		return nil, errors.New("secretName not set in upstream Certificate")
	}

	reqLog.Info("checking for secret " + secretName + " referenced by upstream Certificate")

	// get secret
	secret := &v1.Secret{}
	err = r.Get(ctx, types.NamespacedName{
		Name:      secretName,
		Namespace: upstreamCert.GetNamespace(),
	}, secret)
	if err != nil {
		return nil, err
	}

	return secret, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *CachedCertificateReconciler) SetupWithManager(mgr ctrl.Manager) error {
	indexer := mgr.GetFieldIndexer()

	// index cachedcertificates by upstream ref name when set
	certNameIndexKey := "status.upstreamRef.name"
	err := indexer.IndexField(context.Background(), &cachev1alpha1.CachedCertificate{}, certNameIndexKey, func(o client.Object) []string {
		cert := o.(*cachev1alpha1.CachedCertificate)
		if cert.Status.UpstreamRef != nil && cert.Status.UpstreamRef.Name != "" {
			return []string{cert.Status.UpstreamRef.Name}
		}
		return nil
	})
	if err != nil {
		return err
	}

	// setup the upstream secret reconciler
	// it is a component of this operator and therefore started here
	// rather than independently
	upstreamSecretReconciler := &UpstreamSecretReconciler{
		CacheNamespace:   r.CacheNamespace,
		CertNameIndexKey: certNameIndexKey,
		Client:           r.Client,
		Scheme:           r.Scheme,
	}

	err = upstreamSecretReconciler.SetupWithManager(mgr)
	if err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&cachev1alpha1.CachedCertificate{}).
		Owns(&v1.Secret{}).
		Complete(r)
}
