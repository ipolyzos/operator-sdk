package e2e

import (
	"bytes"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"os/exec"
	"testing"

	"github.com/operator-framework/operator-sdk/test/e2e/e2eutil"
	framework "github.com/operator-framework/operator-sdk/test/e2e/framework"

	core "k8s.io/api/core/v1"
	extensions "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

const (
	filemode = int(0664)
	// amount of lines to remove from end of types file to allow us to fill in the
	// blank structs
	typesFileTrimAmount = 7
)

func TestMemcached(t *testing.T) {
	os.Chdir(os.Getenv("GOPATH") + "/src/github.com/example-inc")
	t.Log("Creating new operator project")
	cmdOut, err := exec.Command("operator-sdk",
		"new",
		"memcached-operator",
		"--api-version=cache.example.com/v1alpha1",
		"--kind=Memcached").CombinedOutput()
	if err != nil {
		t.Fatalf("Error: %v\nCommand Output: %s\n", err, string(cmdOut))
	}

	os.Chdir("memcached-operator")
	os.RemoveAll("vendor/github.com/operator-framework/operator-sdk/pkg")
	os.Symlink(os.Getenv("TRAVIS_BUILD_DIR")+"/pkg", "vendor/github.com/operator-framework/operator-sdk/pkg")
	handlerFile, err := os.Create("pkg/stub/handler.go")
	if err != nil {
		t.Fatal(err)
	}
	defer handlerFile.Close()
	handlerTemplate, err := http.Get("https://raw.githubusercontent.com/operator-framework/operator-sdk/master/example/memcached-operator/handler.go.tmpl")
	if err != nil {
		t.Fatal(err)
	}
	defer handlerTemplate.Body.Close()
	_, err = io.Copy(handlerFile, handlerTemplate.Body)
	if err != nil {
		t.Fatal(err)
	}
	memcachedTypesFile, err := ioutil.ReadFile("pkg/apis/cache/v1alpha1/types.go")
	if err != nil {
		t.Fatal(err)
	}
	memcachedTypesFileLines := bytes.Split(memcachedTypesFile, []byte("\n"))
	memcachedTypesFileLines = memcachedTypesFileLines[:len(memcachedTypesFileLines)-typesFileTrimAmount]
	memcachedTypesFileLines = append(memcachedTypesFileLines, []byte("type MemcachedSpec struct {	Size int32 `json:\"size\"`}"))
	memcachedTypesFileLines = append(memcachedTypesFileLines, []byte("type MemcachedStatus struct {Nodes []string `json:\"nodes\"`}\n"))
	os.Remove("pkg/apis/cache/v1alpha1/types.go")
	err = ioutil.WriteFile("pkg/apis/cache/v1alpha1/types.go", bytes.Join(memcachedTypesFileLines, []byte("\n")), os.FileMode(filemode))
	if err != nil {
		t.Fatal(err)
	}

	t.Log("Generating k8s")
	cmdOut, err = exec.Command("operator-sdk",
		"generate",
		"k8s").CombinedOutput()
	if err != nil {
		t.Fatalf("Error: %v\nCommand Output: %s\n", err, string(cmdOut))
	}

	t.Log("Building operator docker image")
	cmdOut, err = exec.Command("operator-sdk",
		"build",
		"quay.io/example/memcached-operator:v0.0.1").CombinedOutput()
	if err != nil {
		t.Fatalf("Error: %v\nCommand Output: %s\n", err, string(cmdOut))
	}
	operatorYAML, err := ioutil.ReadFile("deploy/operator.yaml")
	if err != nil {
		t.Fatal(err)
	}
	operatorYAML = bytes.Replace(operatorYAML, []byte("imagePullPolicy: Always"), []byte("imagePullPolicy: Never"), 1)
	err = ioutil.WriteFile("deploy/operator.yaml", operatorYAML, os.FileMode(filemode))
	if err != nil {
		t.Fatal(err)
	}

	// get global framework variables
	f := framework.Global
	namespace := "memcached"
	// create namespace
	namespaceObj := &core.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
	_, err = f.KubeClient.CoreV1().Namespaces().Create(namespaceObj)
	if err != nil {
		t.Fatal(err)
	}
	t.Log("Created namespace")

	// create rbac
	rbacYAML, err := ioutil.ReadFile("deploy/rbac.yaml")
	rbacYAMLSplit := bytes.Split(rbacYAML, []byte("\n---\n"))
	for _, rbacSpec := range rbacYAMLSplit {
		err = e2eutil.CreateFromYAML(t, rbacSpec, f.KubeClient, f.KubeConfig, namespace)
		if err != nil {
			t.Fatal(err)
		}
	}
	t.Log("Created rbac")

	// create crd
	crdYAML, err := ioutil.ReadFile("deploy/crd.yaml")
	err = e2eutil.CreateFromYAML(t, crdYAML, f.KubeClient, f.KubeConfig, namespace)
	if err != nil {
		t.Fatal(err)
	}
	t.Log("Created crd")

	// create operator
	operatorYAML, err = ioutil.ReadFile("deploy/operator.yaml")
	err = e2eutil.CreateFromYAML(t, operatorYAML, f.KubeClient, f.KubeConfig, namespace)
	if err != nil {
		t.Fatal(err)
	}
	t.Log("Created operator")

	// wait for memcached-operator to be ready
	err = e2eutil.DeploymentReplicaCheck(t, f.KubeClient, namespace, "memcached-operator", 1, 6)
	if err != nil {
		t.Fatal(err)
	}

	// create example-memcached yaml file
	err = ioutil.WriteFile("deploy/cr.yaml",
		[]byte("apiVersion: \"cache.example.com/v1alpha1\"\nkind: \"Memcached\"\nmetadata:\n  name: \"example-memcached\"\nspec:\n  size: 3"),
		os.FileMode(filemode))
	if err != nil {
		t.Fatal(err)
	}

	// create memcached custom resource
	crYAML, err := ioutil.ReadFile("deploy/cr.yaml")
	e2eutil.CreateFromYAML(t, crYAML, f.KubeClient, f.KubeConfig, namespace)
	memcachedClient := e2eutil.GetCRClient(t, f.KubeConfig, crYAML)

	// wait for example-memcached to reach 3 replicas
	err = e2eutil.DeploymentReplicaCheck(t, f.KubeClient, namespace, "example-memcached", 3, 6)
	if err != nil {
		t.Fatal(err)
	}

	// update memcached CR size to 4
	err = memcachedClient.Patch(types.JSONPatchType).
		Namespace(namespace).
		Resource("memcacheds").
		Name("example-memcached").
		Body([]byte("[{\"op\": \"replace\", \"path\": \"/spec/size\", \"value\": 4}]")).
		Do().
		Error()
	if err != nil {
		t.Fatal(err)
	}

	// wait for example-memcached to reach 4 replicas
	err = e2eutil.DeploymentReplicaCheck(t, f.KubeClient, namespace, "example-memcached", 4, 6)
	if err != nil {
		t.Fatal(err)
	}

	// clean everything up
	err = memcachedClient.Delete().
		Namespace(namespace).
		Resource("memcacheds").
		Name("example-memcached").
		Body([]byte("{\"gracePeriodSeconds\":0}")).
		Do().
		Error()
	if err != nil {
		t.Log("Failed to delete example-memcached CR")
		t.Fatal(err)
	}
	err = f.KubeClient.AppsV1().Deployments(namespace).
		Delete("memcached-operator", metav1.NewDeleteOptions(0))
	if err != nil {
		t.Log("Failed to delete memcached-operator deployment")
		t.Fatal(err)
	}
	err = f.KubeClient.RbacV1beta1().Roles(namespace).Delete("memcached-operator", metav1.NewDeleteOptions(0))
	if err != nil {
		t.Log("Failed to delete memcached-operator Role")
		t.Fatal(err)
	}
	err = f.KubeClient.RbacV1beta1().RoleBindings(namespace).Delete("default-account-memcached-operator", metav1.NewDeleteOptions(0))
	if err != nil {
		t.Log("Failed to delete memcached-operator RoleBinding")
		t.Fatal(err)
	}
	extensionclient, err := extensions.NewForConfig(f.KubeConfig)
	if err != nil {
		t.Fatal(err)
	}
	err = extensionclient.ApiextensionsV1beta1().CustomResourceDefinitions().Delete("memcacheds.cache.example.com", metav1.NewDeleteOptions(0))
	if err != nil {
		t.Log("Failed to delete memcached CRD")
		t.Fatal(err)
	}
	err = f.KubeClient.CoreV1().Namespaces().Delete(namespace, metav1.NewDeleteOptions(0))
	if err != nil {
		t.Log("Failed to delete memcached namespace")
		t.Fatal(err)
	}

	os.RemoveAll(os.Getenv("GOPATH") + "/src/github.com/example-inc/memcached-operator")
}