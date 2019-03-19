package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	extv1beta1 "k8s.io/api/extensions/v1beta1"

	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"

	"github.com/dghubble/sling"
	"github.com/gobwas/glob"
	"github.com/spf13/pflag"
)

const (
	ingressClassAnnotation = "kubernetes.io/ingress.class"
)

var (
	ingressClass   string
	ingressPattern glob.Glob
	kongURL        string
)

func init() {
	pflag.StringVar(&kongURL, "kong-url", "http://localhost:8001", "URL for Kong admin api server")
	pflag.StringVar(&ingressClass, "ingress-class", "kong", "Ingress class being routed through Kong")
	pattern := pflag.String("ingress-pattern", "cm-acme-http-solver-*", "Glob pattern for ingress name")
	pflag.Parse()
	ingressPattern = glob.MustCompile(*pattern)
}

// partial json objects - just the fields we need
type kongRouteFetch struct {
	ID           string   `json:"id"`
	Paths        []string `json:"paths"`
	PreserveHost bool     `json:"preserve_host"`
}
type kongRoutes struct {
	Routes   []kongRouteFetch `json:"data"`
	NextPage string           `json:"next"`
}
type kongRoutePatch struct {
	PreserveHost bool `json:"preserve_host"`
}

func main() {
	config, err := rest.InClusterConfig()
	if err != nil {
		panic(err.Error())
	}
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		panic(err.Error())
	}

	factory := informers.NewSharedInformerFactory(clientset, 2*time.Minute)

	ingressInformer := factory.Extensions().V1beta1().Ingresses().Informer()

	stopper := make(chan struct{})
	defer close(stopper)

	ingressInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			ingress := obj.(*extv1beta1.Ingress)
			class, hasAnnotation := ingress.Annotations[ingressClassAnnotation]
			if ingressPattern.Match(ingress.GetName()) && hasAnnotation && class == ingressClass {
				log.Printf("Matching ingress added: %s", ingress.GetName())
				for _, rule := range ingress.Spec.Rules {
					for _, path := range rule.IngressRuleValue.HTTP.Paths {
						log.Printf("  path %s\n", path.Path)
						go patchKong(path.Path)
					}
				}
			}
		},
	})
	go ingressInformer.Run(stopper)

	sigterm := make(chan os.Signal, 1)
	signal.Notify(sigterm, syscall.SIGTERM)
	signal.Notify(sigterm, syscall.SIGINT)
	<-sigterm
}

func patchKong(path string) {
	// setup a periodic scan (every 10s) of the kong routes for one with the given path
	// when we find it, check that it has preserve_host: true and patch it if not
	// when we get bored of looking for it (after 30s without it), stop

	// this code assumes there aren't very many routes managed by kong; it ignores
	// pagination, and it cycles through them all when it already knows the id and
	// could query directly; needs work if the situation changes...

	missingCount := 0
	foundOnce := false
	for range time.Tick(10 * time.Second) {
		found := false
		routes := &kongRoutes{}
		resp, err := sling.New().Base(kongURL).Get("routes").ReceiveSuccess(routes)
		if err == nil && resp.StatusCode == 200 {
			for _, route := range routes.Routes {
				if len(route.Paths) == 1 && route.Paths[0] == path {
					log.Printf("found matching kong route: %s = %s\n", path, route.ID)
					found = true
					if route.PreserveHost == false {
						kongPatch := &kongRoutePatch{
							PreserveHost: true,
						}
						patched := &kongRouteFetch{}
						resp, err := sling.New().Base(kongURL).Patch(fmt.Sprintf("routes/%s", route.ID)).BodyJSON(kongPatch).ReceiveSuccess(patched)
						if err == nil && resp.StatusCode == 200 {
							log.Printf("successfully patched kong route: %s\n", patched.ID)
						} else if err != nil {
							log.Printf("failed to patch kong route: %s\n", err.Error())
						} else {
							log.Printf("failed to patch kong route: [%d] %s\n", resp.StatusCode, resp.Status)
						}
					} else {
						log.Printf("nothing to do; route for %s already has preserve_host set\n", path)
					}
				}
			}
		} else {
			log.Printf("failed to query kong for routes: [%d] %s\n", resp.StatusCode, err.Error())
		}
		if found {
			foundOnce = true
			missingCount = 0
		} else {
			missingCount = missingCount + 1
			if missingCount == 3 {
				if foundOnce {
					log.Printf("mission accomplished for path %s\n", path)
				} else {
					log.Printf("mission *FAILED* for path %s\n", path)
				}
				return
			}
		}
	}
}
