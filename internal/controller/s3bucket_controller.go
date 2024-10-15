/*
Copyright 2024.

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

package controller

import (
	"context"
	"errors"
	"fmt"
	"os"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	"github.com/go-logr/logr"

	localstackv1 "github.com/jiaqi-yin/localstack-controller/api/v1"
)

// Add a finalizer for the resource
const finalizerName = "s3bucket.localstack.kb.dev/finalizer"

// S3BucketReconciler reconciles a S3Bucket object
type S3BucketReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=localstack.kb.dev,resources=s3buckets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=localstack.kb.dev,resources=s3buckets/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=localstack.kb.dev,resources=s3buckets/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the S3Bucket object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.19.0/pkg/reconcile
func (r *S3BucketReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	existingBucket := &localstackv1.S3Bucket{}
	if err := r.Get(ctx, req.NamespacedName, existingBucket); err != nil {
		logger.Error(err, "Failed to get S3Bucket")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	s3Client, err := NewS3Client(logger)
	if err != nil {
		return ctrl.Result{}, err
	}

	bucketName := existingBucket.ObjectMeta.Namespace + existingBucket.ObjectMeta.Name
	if existingBucket.Status.Location == "" {
		// Create the desired Bucket
		desiredBucket, err := s3Client.CreateBucket(ctx, bucketName, existingBucket.Spec.Region)
		if err != nil {
			return ctrl.Result{}, err
		}

		// Update status
		existingBucket.Status.Location = *desiredBucket.Location
		if err = r.Status().Update(ctx, existingBucket); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Handle deletion if the resource is marked for deletion
	if existingBucket.ObjectMeta.DeletionTimestamp.IsZero() {
		// The resource is not marked for deletion, add the finalizer
		if !controllerutil.ContainsFinalizer(existingBucket, finalizerName) {
			controllerutil.AddFinalizer(existingBucket, finalizerName)
			if err := r.Update(ctx, existingBucket); err != nil {
				return ctrl.Result{}, err
			}
		}
	} else {
		// The resource is marked for deletion, handle cleanup
		if controllerutil.ContainsFinalizer(existingBucket, finalizerName) {
			// Delete S3 bucket and ensure that deletion is idempotent
			exists, err := s3Client.BucketExists(ctx, bucketName)
			if err != nil {
				return ctrl.Result{}, err
			}

			if !exists {
				return ctrl.Result{}, nil
			}

			err = s3Client.DeleteBucket(ctx, bucketName)
			if err != nil {
				logger.Error(err, "Failed to delete bucket")
				return ctrl.Result{}, err
			}

			// Remove the finalizer
			controllerutil.RemoveFinalizer(existingBucket, finalizerName)
			if err := r.Update(ctx, existingBucket); err != nil {
				return ctrl.Result{}, err
			}
		}
	}

	return ctrl.Result{}, nil
}

func NewS3Client(logger logr.Logger) (BucketBasics, error) {
	awsEndpoint := os.Getenv("AWS_ENDPOINT_URL")
	awsRegion := os.Getenv("AWS_DEFAULT_REGION")
	awsCfg, err := config.LoadDefaultConfig(context.TODO(),
		config.WithRegion(awsRegion),
	)
	if err != nil {
		return BucketBasics{}, err
	}

	// Create the resource s3Client
	s3Client := BucketBasics{
		S3Client: s3.NewFromConfig(awsCfg, func(o *s3.Options) {
			o.UsePathStyle = true
			o.BaseEndpoint = aws.String(awsEndpoint)
		}),
		Logger: logger,
	}
	return s3Client, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *S3BucketReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&localstackv1.S3Bucket{}).
		Complete(r)
}

// BucketBasics encapsulates the Amazon Simple Storage Service (Amazon S3) actions
// used in the examples.
// It contains S3Client, an Amazon S3 service client that is used to perform bucket
// and object actions.
type BucketBasics struct {
	S3Client *s3.Client
	Logger   logr.Logger
}

// BucketExists checks whether a bucket exists in the current account.
func (basics BucketBasics) BucketExists(ctx context.Context, bucketName string) (bool, error) {
	_, err := basics.S3Client.HeadBucket(ctx, &s3.HeadBucketInput{
		Bucket: aws.String(bucketName),
	})
	exists := true
	if err != nil {
		var apiError smithy.APIError
		if errors.As(err, &apiError) {
			var notFound *types.NotFound
			switch {
			case errors.As(apiError, &notFound):
				basics.Logger.Info("Bucket is not found.", "bucketName", bucketName)
				exists = false
				err = nil
			default:
				basics.Logger.Error(err, "Either you don't have access to bucket or another error occurred.", "bucketName", bucketName)
			}
		}
	} else {
		basics.Logger.Info("Bucket exists and you already own it.", "bucketName", bucketName)
	}

	return exists, err
}

// CreateBucket creates a bucket with the specified name in the specified Region.
func (basics BucketBasics) CreateBucket(ctx context.Context, name string, region string) (*s3.CreateBucketOutput, error) {
	bucket, err := basics.S3Client.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket: aws.String(name),
		CreateBucketConfiguration: &types.CreateBucketConfiguration{
			LocationConstraint: types.BucketLocationConstraint(region),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("Couldn't create bucket %v in Region %v. Here's why: %v\n", name, region, err)
	}
	return bucket, err
}

// DeleteBucket deletes a bucket. The bucket must be empty or an error is returned.
func (basics BucketBasics) DeleteBucket(ctx context.Context, bucketName string) error {
	_, err := basics.S3Client.DeleteBucket(ctx, &s3.DeleteBucketInput{
		Bucket: aws.String(bucketName)})
	if err != nil {
		basics.Logger.Error(err, "Couldn't delete bucket.", "bucketName", bucketName)
	}
	return err
}
