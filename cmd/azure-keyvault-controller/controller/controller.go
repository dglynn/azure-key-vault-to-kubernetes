/*
Copyright Sparebanken Vest

Based on the Kubernetes controller example at
https://github.com/kubernetes/sample-controller

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
	"time"

	"github.com/appscode/go/runtime"
	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"

	corev1 "k8s.io/api/core/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/cache"
	"kmodules.xyz/client-go/tools/queue"

	// "k8s.io/client-go/util/workqueue"

	akv "github.com/SparebankenVest/azure-key-vault-to-kubernetes/pkg/k8s/apis/azurekeyvault/v2alpha1"
	akvcs "github.com/SparebankenVest/azure-key-vault-to-kubernetes/pkg/k8s/client/clientset/versioned"
	keyvaultScheme "github.com/SparebankenVest/azure-key-vault-to-kubernetes/pkg/k8s/client/clientset/versioned/scheme"
	akvInformers "github.com/SparebankenVest/azure-key-vault-to-kubernetes/pkg/k8s/client/informers/externalversions"
	akvInformersv2alpha1 "github.com/SparebankenVest/azure-key-vault-to-kubernetes/pkg/k8s/client/informers/externalversions/azurekeyvault/v2alpha1"
)

const (
	// SuccessSynced is used as part of the Event 'reason' when a AzureKeyVaultSecret is synced
	SuccessSynced = "Synced"

	// ErrResourceExists is used as part of the Event 'reason' when a AzureKeyVaultSecret fails
	// to sync due to a Secret of the same name already existing.
	ErrResourceExists = "ErrResourceExists"

	// ErrAzureVault is used as part of the Event 'reason' when a AzureKeyVaultSecret fails
	// to sync due to a Secret of the same name already existing.
	ErrAzureVault = "ErrAzureVault"

	// FailedAzureKeyVault is the message used for Events when a resource
	// fails to get secret from Azure Key Vault
	FailedAzureKeyVault = "Failed to get secret for '%s' from Azure Key Vault '%s'"

	// MessageResourceExists is the message used for Events when a resource
	// fails to sync due to a Deployment already existing
	MessageResourceExists = "Resource '%s' already exists and is not managed by AzureKeyVaultSecret"

	// MessageResourceSynced is the message used for an Event fired when a AzureKeyVaultSecret
	// is synced successfully
	MessageResourceSynced = "AzureKeyVaultSecret synced successfully"

	// MessageResourceSyncedWithAzure is the message used for an Event fired when a AzureKeyVaultSecret
	// is synced successfully after getting updated secret from Azure Key Vault
	MessageResourceSyncedWithAzure = "AzureKeyVaultSecret synced successfully with Azure Key Vault"
)

// Controller is the controller implementation for AzureKeyVaultSecret resources
type Controller struct {
	// Handler process work on workqueues
	handler *Handler

	// secretsSynced              cache.InformerSynced
	// azureKeyVaultSecretsSynced cache.InformerSynced

	// workqueue is a rate limited work queue. This is used to queue work to be
	// processed instead of performing it as soon as a change happens. This
	// means we can ensure we only process a fixed amount of resources at a
	// time, and makes it easy to ensure we are never processing the same item
	// simultaneously in two different workers.

	secretInformerFactory informers.SharedInformerFactory
	akvsInformerFactory   akvInformers.SharedInformerFactory

	secretInformer cache.SharedIndexInformer
	secretQueue    *queue.Worker

	akvsInformer cache.SharedIndexInformer
	akvsCrdQueue *queue.Worker //workqueue.RateLimitingInterface

	akvQueue *queue.Worker //workqueue.RateLimitingInterface
}

// Options contains options for the controller
type Options struct {
	NumThreads     int
	MaxNumRequeues int
	ResyncPeriod   time.Duration
	AkvsRef        corev1.ObjectReference
}

// NewController returns a new AzureKeyVaultSecret controller
func NewController(client kubernetes.Interface, akvsClient akvcs.Interface, handler *Handler, azureFrequency AzurePollFrequency, options *Options) *Controller {
	// Create event broadcaster
	// Add azure-keyvault-controller types to the default Kubernetes Scheme so Events can be
	// logged for azure-keyvault-controller types.
	utilruntime.Must(keyvaultScheme.AddToScheme(scheme.Scheme))

	controller := &Controller{
		handler: handler,
		// secretsSynced:              secretInformer.Informer().HasSynced,
		// azureKeyVaultSecretsSynced: azureKeyVaultSecretsInformer.Informer().HasSynced,

		//workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "AzureKeyVaultSecrets"),
		//workqueue.NewNamedRateLimitingQueue(workqueue.NewItemFastSlowRateLimiter(azureFrequency.Normal, azureFrequency.Slow, azureFrequency.MaxFailuresBeforeSlowingDown), "AzureKeyVault"),

		akvsInformerFactory:   akvInformers.NewFilteredSharedInformerFactory(akvsClient, options.ResyncPeriod, options.AkvsRef.Namespace, nil),
		secretInformerFactory: informers.NewFilteredSharedInformerFactory(client, options.ResyncPeriod, options.AkvsRef.Namespace, nil),
		secretQueue:           queue.New("Secrets", options.MaxNumRequeues, options.NumThreads, handler.syncSecret),
		akvsCrdQueue:          queue.New("AzureKeyVaultSecrets", options.MaxNumRequeues, options.NumThreads, handler.syncAzureKeyVaultSecret),
		akvQueue:              queue.New("AzureKeyVault", options.MaxNumRequeues, options.NumThreads, handler.syncAzureKeyVault),
	}

	log.Info("Setting up event handlers")
	// Set up an event handler for when AzureKeyVaultSecret resources change
	akvsInformer := controller.akvsInformerFactory.InformerFor(&akv.AzureKeyVaultSecret{}, func(client akvcs.Interface, resyncPeriod time.Duration) cache.SharedIndexInformer {
		return akvInformersv2alpha1.NewAzureKeyVaultSecretInformer(
			akvsClient,
			options.AkvsRef.Namespace,
			resyncPeriod,
			cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc},
		)
	})
	akvsInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			if secret, ok := obj.(*akv.AzureKeyVaultSecret); ok {
				if secret.Spec.Output.Secret.Name == "" {
					return
				}
				log.Debugf("AzureKeyVaultSecret '%s' added. Adding to queue.", secret.Name)
				queue.Enqueue(controller.akvsCrdQueue.GetQueue(), obj)
				queue.Enqueue(controller.akvQueue.GetQueue(), obj)
				// controller.enqueueAzureKeyVaultSecret(obj)
				// controller.enqueueAzurePoll(obj)
			}
		},
		UpdateFunc: func(old, new interface{}) {
			if newSecret, ok := new.(*akv.AzureKeyVaultSecret); ok {
				oldSecret := old.(*akv.AzureKeyVaultSecret)
				if oldSecret.Spec.Output.Secret.Name == "" {
					return
				}
				if newSecret.ResourceVersion == oldSecret.ResourceVersion {
					log.Debugf("AzureKeyVaultSecret '%s' added to Azure queue to check if changed in Azure.", newSecret.Name)
					// Check if secret has changed in Azure
					queue.Enqueue(controller.akvQueue.GetQueue(), new)
					// controller.enqueueAzurePoll(new)
					return
				}

				log.Debugf("AzureKeyVaultSecret '%s' changed. Adding to queue.", newSecret.Name)
				queue.Enqueue(controller.akvsCrdQueue.GetQueue(), new)
				// controller.enqueueAzureKeyVaultSecret(new)
			}
		},
		DeleteFunc: func(obj interface{}) {
			if secret, ok := obj.(*akv.AzureKeyVaultSecret); ok {
				if secret.Spec.Output.Secret.Name == "" {
					return
				}
				log.Debugf("AzureKeyVaultSecret '%s' deleted. Adding to delete queue.", secret.Name)
				queue.Enqueue(controller.akvsCrdQueue.GetQueue(), obj)
				// controller.enqueueDeleteAzureKeyVaultSecret(obj)
			}
		},
	})

	// Set up an event handler for when Secret resources change. This
	// handler will lookup the owner of the given Secret, and if it is
	// owned by a AzureKeyVaultSecret resource will enqueue that Secret resource for
	// processing. This way, we don't need to implement custom logic for
	// handling AzureKeyVaultSecret resources. More info on this pattern:
	// https://github.com/kubernetes/community/blob/8cafef897a22026d42f5e5bb3f104febe7e29830/contributors/devel/controllers.md
	controller.secretInformerFactory.Core().V1().Secrets().Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			if secret, ok := obj.(*corev1.Secret); ok {
				log.Debugf("Secret '%s' added. Handling.", secret.Name)
				queue.Enqueue(controller.secretQueue.GetQueue(), obj)
				// controller.enqueueObject(obj)
			}
		},
		UpdateFunc: func(old, new interface{}) {
			if newSecret, ok := new.(*corev1.Secret); ok {
				oldSecret := old.(*corev1.Secret)

				if newSecret.ResourceVersion == oldSecret.ResourceVersion {
					// Periodic resync will send update events for all known Secrets.
					// Two different versions of the same Secret will always have different RVs.
					return
				}
				secret := new.(*corev1.Secret)
				log.Debugf("Secret '%s' controlled by AzureKeyVaultSecret changed. Handling.", secret.Name)
				queue.Enqueue(controller.secretQueue.GetQueue(), new)
				// controller.enqueueObject(new)
			}
		},
		DeleteFunc: func(obj interface{}) {
			if secret, ok := obj.(*corev1.Secret); ok {
				log.Debugf("Secret '%s' deleted. Handling.", secret.Name)
				queue.Enqueue(controller.secretQueue.GetQueue(), obj)
				// controller.enqueueObject(obj)
			}
		},
	})

	return controller
}

// Run will set up the event handlers for types we are interested in, as well
// as syncing informer caches and starting workers. It will block until stopCh
// is closed, at which point it will shutdown the workqueue and wait for
// workers to finish processing their current work items.
func (c *Controller) Run(stopCh <-chan struct{}) {
	defer utilruntime.HandleCrash()
	// defer c.workqueue.ShutDown()
	// defer c.workqueueAzure.ShutDown()

	// Start the informer factories to begin populating the informer caches
	log.Info("Starting AzureKeyVaultSecret controller")
	c.akvsInformerFactory.Start(stopCh)
	c.secretInformerFactory.Start(stopCh)

	// Wait for all involved caches to be synced, before processing items from the queue is started
	for _, v := range c.akvsInformerFactory.WaitForCacheSync(stopCh) {
		if !v {
			runtime.HandleError(errors.Errorf("timed out waiting for caches to sync"))
			return
		}
	}
	for _, v := range c.secretInformerFactory.WaitForCacheSync(stopCh) {
		if !v {
			runtime.HandleError(errors.Errorf("timed out waiting for caches to sync"))
			return
		}
	}

	c.akvQueue.Run(stopCh)
	c.secretQueue.Run(stopCh)
	c.akvQueue.Run(stopCh)

	// // Wait for the caches to be synced before starting workers
	// log.Info("Waiting for informer caches to sync")
	// if ok := cache.WaitForCacheSync(stopCh, c.secretsSynced, c.azureKeyVaultSecretsSynced); !ok {
	// 	return fmt.Errorf("failed to wait for caches to sync")
	// }

	// log.Info("Starting workers")
	// // Launch two workers to process AzureKeyVaultSecret resources
	// for i := 0; i < threadiness; i++ {
	// 	go wait.Until(c.runWorker, time.Second, stopCh)
	// 	go wait.Until(c.runAzureWorker, time.Second, stopCh)
	// }

	log.Info("Started workers")
	<-stopCh
	log.Info("Shutting down workers")
}

// // runWorker is a long-running function that will continually call the
// // processNextWorkItem function in order to read and process a message on the
// // workqueue.
// func (c *Controller) runWorker() {
// 	for c.processNextWorkItem(c.workqueue, false) {
// 	}
// }

// func (c *Controller) runAzureWorker() {
// 	for c.processNextWorkItem(c.workqueueAzure, true) {
// 	}
// }

// // processNextWorkItem will read a single work item off the workqueue and
// // attempt to process it, by calling the syncHandler.
// func (c *Controller) processNextWorkItem(queue workqueue.RateLimitingInterface, syncAzure bool) bool {
// 	log.Debug("Processing next work item in queue...")
// 	obj, shutdown := queue.Get()

// 	if shutdown {
// 		return false
// 	}

// 	// We wrap this block in a func so we can defer c.workqueue.Done.
// 	err := func(obj interface{}) error {
// 		defer queue.Done(obj)
// 		var key string
// 		var ok bool
// 		var successMsg string

// 		if key, ok = obj.(string); !ok {
// 			queue.Forget(obj)
// 			utilruntime.HandleError(fmt.Errorf("expected string in workqueue but got %#v", obj))
// 			return nil
// 		}

// 		var err error
// 		if syncAzure {
// 			log.Debugf("Handling '%s' in Azure queue...", key)
// 			successMsg = "Successfully synced AzureKeyVaultSecret '%s' with Azure Key Vault"
// 			err = c.handler.azureSyncHandler(key)
// 		} else {
// 			log.Debugf("Handling '%s' in default queue...", key)
// 			successMsg = "Successfully synced AzureKeyVaultSecret '%s' with Kubernetes Secret"
// 			err = c.handler.kubernetesSyncHandler(key)
// 		}

// 		if err != nil {
// 			queue.AddRateLimited(key)
// 			return fmt.Errorf("error syncing '%s': %s, requeuing", key, err.Error())
// 		}

// 		queue.Forget(obj)
// 		log.Infof(successMsg, key)
// 		return nil
// 	}(obj)

// 	if err != nil {
// 		log.Error(err)
// 		return true
// 	}

// 	return true
// }

// func (c *Controller) enqueueObject(obj interface{}) {
// 	azureKeyVaultSecret, ignore, err := c.handler.handleObject(obj)

// 	if err != nil {
// 		utilruntime.HandleError(err)
// 	}

// 	if !ignore {
// 		c.enqueueAzureKeyVaultSecret(azureKeyVaultSecret)
// 	}
// }

// // enqueueAzureKeyVaultSecret takes a AzureKeyVaultSecret resource and converts it into a namespace/name
// // string which is then put onto the work queue. This method should *not* be
// // passed resources of any type other than AzureKeyVaultSecret.
// func (c *Controller) enqueueAzureKeyVaultSecret(obj interface{}) {
// 	var key string
// 	var err error
// 	if key, err = cache.MetaNamespaceKeyFunc(obj); err != nil {
// 		utilruntime.HandleError(err)
// 		return
// 	}
// 	c.akvsCrdQueue.AddRateLimited(key)
// }

// // enqueueAzurePoll takes a AzureKeyVaultSecret resource and converts it into a namespace/name
// // string which is then put onto the work queue. This method should *not* be
// // passed resources of any type other than AzureKeyVaultSecret.
// func (c *Controller) enqueueAzurePoll(obj interface{}) {
// 	var key string
// 	var err error
// 	if key, err = cache.MetaNamespaceKeyFunc(obj); err != nil {
// 		utilruntime.HandleError(err)
// 		return
// 	}
// 	c.akvQueue.AddRateLimited(key)
// }

// // dequeueAzureKeyVaultSecret takes a AzureKeyVaultSecret resource and converts it into a namespace/name
// // string which is then put onto the work queue for deltion. This method should *not* be
// // passed resources of any type other than AzureKeyVaultSecret.
// func (c *Controller) enqueueDeleteAzureKeyVaultSecret(obj interface{}) {
// 	var key string
// 	var err error

// 	if key, err = cache.DeletionHandlingMetaNamespaceKeyFunc(obj); err != nil {
// 		utilruntime.HandleError(err)
// 		return
// 	}
// 	c.akvsCrdQueue.AddRateLimited(key)

// 	// Getting default key to remove from Azure work queue
// 	if key, err = cache.MetaNamespaceKeyFunc(obj); err != nil {
// 		utilruntime.HandleError(err)
// 		return
// 	}
// 	c.akvQueue.Forget(key)
// }
