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

	corev1 "k8s.io/api/core/v1"
	k8serr "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	cachev1alpha1 "weavelab.xyz/cached-certificate-operator/api/v1alpha1"
)

const (
	// CertificateNameAnnotationKey is the label key used by cert-manager to indicate the source certificate for a secret
	CertificateNameAnnotationKey = "cert-manager.io/certificate-name"
)

// UpstreamSecretReconciler triggers the reconcile of CachedCertificate objects as the upstream secrets change
type UpstreamSecretReconciler struct {
	CacheNamespace   string
	CertNameIndexKey string

	client.Client
	Scheme *runtime.Scheme
}

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *UpstreamSecretReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	reqLog := log.FromContext(ctx)

	secret := &corev1.Secret{}
	err := r.Get(ctx, req.NamespacedName, secret)
	switch {
	case k8serr.IsNotFound(err):
		// nothing to do so exit with requeue and no err
		return ctrl.Result{}, nil
	case err != nil:
		return ctrl.Result{}, err
	}

	certName := secret.Annotations[CertificateNameAnnotationKey]
	if certName == "" {
		// nothing to do so exit with requeue and no err
		return ctrl.Result{}, nil
	}

	// get a list of all certs using the updated secret, using the indexed attribute for fast listings
	certList := &cachev1alpha1.CachedCertificateList{}
	err = r.List(ctx, certList, client.MatchingFields{r.CertNameIndexKey: certName})
	if err != nil {
		return ctrl.Result{Requeue: true}, err
	}

	for _, cert := range certList.Items {
		reqLog.Info("Updating upstream cert to pending status to trigger reconcile", "cert_name", cert.GetName(), "cert_namespace", cert.GetNamespace())
		patch := client.MergeFrom(cert.DeepCopy())
		cert.Status.State = cachev1alpha1.CachedCertificateStatePending
		err := r.Client.Status().Patch(ctx, &cert, patch)
		if err != nil {
			return reconcile.Result{RequeueAfter: time.Second * 3}, err
		}
	}

	return reconcile.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager. It will force reconciles only for secrets in the given namespace
func (r *UpstreamSecretReconciler) SetupWithManager(mgr ctrl.Manager) error {
	namespaceAndLabelsPredicate := predicate.NewPredicateFuncs(
		func(object client.Object) bool {
			return object.GetNamespace() == r.CacheNamespace && // in the cache namespace
				object.GetAnnotations()[CertificateNameAnnotationKey] != "" && // owned by cert-manager
				object.GetLabels()[SyncedLabelKey] != "true" // not made by us (usually only happens in local dev)
		},
	)

	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Secret{}, builder.WithPredicates(
			predicate.And(
				ResourceVersionChangesOnly{}, // only reconcile on actual resource version changes, meaning we skip all initial add reconciles
				namespaceAndLabelsPredicate,  // only watch the cached namespace for secrets not owned by us
			),
		)).
		Complete(r)
}
