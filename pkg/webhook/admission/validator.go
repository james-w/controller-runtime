/*
Copyright 2018 The Kubernetes Authors.

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

package admission

import (
	"context"
	"net/http"

	"k8s.io/api/admission/v1beta1"
	"k8s.io/apimachinery/pkg/runtime"
)

// Validator defines functions for validating an operation
type Validator interface {
	runtime.Object
	ValidateCreate() error
	ValidateUpdate(old runtime.Object) error
	ValidateDelete() error
}

// MetaValidator defines functions for validating an operation that also
// receive the request, giving access to e.g. the user info
type MetaValidator interface {
	// DeepCopyObject returns an empty object of the correct type
	DeepCopyObject() runtime.Object
	// ValidateCreate validates that the passed object can be created,
	// and allows for the request to be examined
	ValidateCreate(runtime.Object, v1beta1.AdmissionRequest) error
	// ValidateUpdate validates that the object can be updated from `old` to `obj`,
	// and allows for the request to be examined
	ValidateUpdate(obj runtime.Object, old runtime.Object, req v1beta1.AdmissionRequest) error
	// ValidateCreate validates that the passed object can be deleted,
	// and allows for the request to be examined
	ValidateDelete(runtime.Object, v1beta1.AdmissionRequest) error
}

// ValidatorWrapper wraps a Validator in a MetaValidator for compatibility
type ValidatorWrapper struct {
	Validator Validator
}

// NewValidatorWrapper creates a MetaValidator out of a Validator using a ValidatorWrapper
func NewValidatorWrapper(validator Validator) MetaValidator {
	return &ValidatorWrapper{Validator: validator}
}

// DeepCopyObject creates an empty object of the correct type. It delegates to
// DeepCopyObject of the underlying Validator
func (v *ValidatorWrapper) DeepCopyObject() runtime.Object {
	return v.Validator.DeepCopyObject()
}

// ValidateCreate checks that `obj` can be created. It delegates to calling `ValidateCreate`
// on the object, and assumes that it implements Validator
func (v *ValidatorWrapper) ValidateCreate(obj runtime.Object, _ v1beta1.AdmissionRequest) error {
	return obj.(Validator).ValidateCreate()
}

// ValidateDelete checks that `obj` can be deleted. It delegates to calling `ValidateDelete`
// on the object, and assumes that it implements Validator
func (v *ValidatorWrapper) ValidateDelete(obj runtime.Object, _ v1beta1.AdmissionRequest) error {
	return obj.(Validator).ValidateDelete()
}

// ValidateUpdate checks that `obj` can be updated. It delegates to calling `ValidateUpdate`
// on the object, and assumes that it implements Validator
func (v *ValidatorWrapper) ValidateUpdate(obj, old runtime.Object, _ v1beta1.AdmissionRequest) error {
	return obj.(Validator).ValidateUpdate(old)
}

var _ MetaValidator = &ValidatorWrapper{}

// ValidatingWebhookFor creates a new Webhook for validating the provided type.
func ValidatingWebhookFor(validator MetaValidator) *Webhook {
	return &Webhook{
		Handler: &validatingHandler{validator: validator},
	}
}

type validatingHandler struct {
	validator MetaValidator
	decoder   *Decoder
}

var _ DecoderInjector = &validatingHandler{}

// InjectDecoder injects the decoder into a validatingHandler.
func (h *validatingHandler) InjectDecoder(d *Decoder) error {
	h.decoder = d
	return nil
}

// Handle handles admission requests.
func (h *validatingHandler) Handle(ctx context.Context, req Request) Response {
	if h.validator == nil {
		panic("validator should never be nil")
	}

	// Get the object in the request
	obj := h.validator.DeepCopyObject()
	if req.Operation == v1beta1.Create {
		err := h.decoder.Decode(req, obj)
		if err != nil {
			return Errored(http.StatusBadRequest, err)
		}

		err = h.validator.ValidateCreate(obj, req.AdmissionRequest)
		if err != nil {
			return Denied(err.Error())
		}
	}

	if req.Operation == v1beta1.Update {
		oldObj := obj.DeepCopyObject()

		err := h.decoder.DecodeRaw(req.Object, obj)
		if err != nil {
			return Errored(http.StatusBadRequest, err)
		}
		err = h.decoder.DecodeRaw(req.OldObject, oldObj)
		if err != nil {
			return Errored(http.StatusBadRequest, err)
		}

		err = h.validator.ValidateUpdate(obj, oldObj, req.AdmissionRequest)
		if err != nil {
			return Denied(err.Error())
		}
	}

	if req.Operation == v1beta1.Delete {
		// In reference to PR: https://github.com/kubernetes/kubernetes/pull/76346
		// OldObject contains the object being deleted
		err := h.decoder.DecodeRaw(req.OldObject, obj)
		if err != nil {
			return Errored(http.StatusBadRequest, err)
		}

		err = h.validator.ValidateDelete(obj, req.AdmissionRequest)
		if err != nil {
			return Denied(err.Error())
		}
	}

	return Allowed("")
}
