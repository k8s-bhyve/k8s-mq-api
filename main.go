// CBSD Project 2013-2020
// K8s-bhyve project 2020
// Simple demo/sample for CBSD K8S API
package main

import (
	"encoding/json"
	"github.com/gorilla/mux"
	"log"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"fmt"
	"reflect"
	"flag"
)

var lock = sync.RWMutex{}
var config Config
var runscript string
var workdir string

type Response struct {
	Message    string
}

// The cluster Type. Name of elements must match with jconf params
type Cluster struct {
	K8s_name		string	`json:jname,omitempty"`
	Init_masters		string	`json:init_masters,omitempty"`
	Init_workers		string	`json:init_workers,omitempty"`
	Master_vm_ram		string	`json:master_vm_ram,omitempty"`
	Master_vm_cpus		string	`"master_vm_cpus,omitempty"`
	Master_vm_imgsize	string	`"master_vm_imgsize,omitempty"`
	Worker_vm_ram		string	`"worker_vm_ram,omitempty"`
	Worker_vm_cpus		string	`"worker_vm_cpus,omitempty"`
	Worker_vm_imgsize	string	`"worker_vm_imgsize,omitempty"`
	Pv_enable		string	`"pv_enable,omitempty"`
	Kubelet_master		string	`"kubelet_master,omitempty"`
	Email			string	`"email,omitempty"`
	Callback		string	`"callback,omitempty"`
}
// Todo: validate mod?
//  e.g for simple check:
//  K8s_name  string `json:"name" validate:"required,min=2,max=100"`

var (
	body		= flag.String("body", "", "Body of message")
	cbsdEnv		= flag.String("cbsdenv", "/usr/jails", "CBSD workdir environment")
	configFile	= flag.String("config", "/usr/local/etc/cbsd-mq-router.json", "Path to config.json")
	runScript	= flag.String("runscript", "k8s", "CBSD K8S target run script")
	listen *string	= flag.String("listen", "127.0.0.1:8081", "Listen host:port")
)

func fileExists(filename string) bool {
	info, err := os.Stat(filename)
	if os.IsNotExist(err) {
		return false
	}
	return !info.IsDir()
}

// main function to boot up everything
func main() {

	flag.Parse()
	var err error

	config, err = LoadConfiguration(*configFile)

	runscript = *runScript
	workdir=config.CbsdEnv

	if err != nil {
		fmt.Println("config load error")
		os.Exit(1)
	}

	router := mux.NewRouter()
	router.HandleFunc("/api/v1/create/{instanceid}", HandleClusterCreate).Methods("POST")
	router.HandleFunc("/api/v1/status/{instanceid}", HandleClusterStatus).Methods("GET")
	router.HandleFunc("/api/v1/destroy/{instanceid}", HandleClusterDestroy).Methods("GET")
	fmt.Println("Listen",*listen)
	log.Fatal(http.ListenAndServe(*listen, router))
}

func HandleClusterStatus(w http.ResponseWriter, r *http.Request) {
	var instanceid string
	params := mux.Vars(r)
	instanceid = params["instanceid"]
	var regexpInstanceId = regexp.MustCompile(`^[aA-zZ_]([aA-zZ0-9_])*$`)

	// check the name field is between 3 to 40 chars
	if len(instanceid) < 3 || len(instanceid) > 40 {
		http.Error(w, "The instance name must be between 3-40", 400)
		return
	}
	if !regexpInstanceId.MatchString(instanceid) {
		http.Error(w, "The instance name should be valid form, ^[aA-zZ_]([aA-zZ0-9_])*$", 400)
		return
	}
	SqliteDBPath := fmt.Sprintf("%s/var/db/k8s/%s.sqlite", workdir,instanceid)
	if fileExists(SqliteDBPath) {
		http.Error(w, "{ status: \"exist\"}", 400)
		return
	} else {
		http.Error(w, "{}", 400)
	}
}

func realInstanceCreate(body string) {

	a := &body
	stdout, err := beanstalkSend(config.BeanstalkConfig, *a)
	fmt.Printf("%s\n",stdout);

	if err != nil {
		return
	}
}

func getStructTag(f reflect.StructField) string {
	return string(f.Tag)
}

func HandleClusterCreate(w http.ResponseWriter, r *http.Request) {
	var instanceid string
	params := mux.Vars(r)
	instanceid = params["instanceid"]
	var regexpInstanceId = regexp.MustCompile(`^[aA-zZ_]([aA-zZ0-9_])*$`)
	var regexpSize = regexp.MustCompile(`^[1-9](([0-9]+)?)([m|g|t])$`)
	var regexpEmail = regexp.MustCompile("^[a-zA-Z0-9.!#$%&'*+/=?^_`{|}~-]+@[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?(?:\\.[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)*$")
	var regexpCallback = regexp.MustCompile(`^(http|https)://`)

	w.Header().Set("Content-Type", "application/json")

	// check the name field is between 3 to 40 chars
	if len(instanceid) < 3 || len(instanceid) > 40 {
		response := Response{"The instance name must be between 3-40"}
		js, err := json.Marshal(response)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		http.Error(w, string(js), 400)
		return
	}
	if !regexpInstanceId.MatchString(instanceid) {
		response := Response{"The instance name should be valid form, ^[aA-zZ_]([aA-zZ0-9_])*$"}
		js, err := json.Marshal(response)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		http.Error(w, string(js), 400)
		return
	}

	// check for existance
	// todo: to API
	SqliteDBPath := fmt.Sprintf("%s/var/db/k8s/%s.sqlite", workdir,instanceid)
	if fileExists(SqliteDBPath) {
		response := Response{"Cluster already exist"}
		js, err := json.Marshal(response)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		http.Error(w, string(js), 400)
		return
	}

	if r.Body == nil {
		response := Response{"Please send a request body"}
		js, err := json.Marshal(response)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		http.Error(w, string(js), 400)
		return
	}

	var cluster Cluster
	_ = json.NewDecoder(r.Body).Decode(&cluster)
	json.NewEncoder(w).Encode(cluster)

	if !regexpEmail.MatchString(cluster.Email) {
		response := Response{"email should be valid form"}
		js, err := json.Marshal(response)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		http.Error(w, string(js), 400)
		return
	}

	if !regexpCallback.MatchString(cluster.Callback) {
		response := Response{"callback should be valid form"}
		js, err := json.Marshal(response)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		http.Error(w, string(js), 400)
		return
	}

	// master value validation
	init_masters, err := strconv.Atoi(cluster.Init_masters)
	if err != nil {
		response := Response{"Init_masters not a number"}
		js, err := json.Marshal(response)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		http.Error(w, string(js), 400)
		return
	}
	if init_masters <= 0 || init_masters > 10 {
		response := Response{"Init_masters valid range: 1-10"}
		js, err := json.Marshal(response)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		http.Error(w, string(js), 400)
		return
	}
	if !regexpSize.MatchString(cluster.Master_vm_ram) {
		response := Response{"The master_vm_ram should be valid form, 512m, 1g"}
		js, err := json.Marshal(response)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		http.Error(w, string(js), 400)
		return
	}
	if !regexpSize.MatchString(cluster.Master_vm_imgsize) {
		response := Response{"The master_vm_imgsize should be valid form, 2g, 30g"}
		js, err := json.Marshal(response)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		http.Error(w, string(js), 400)
		return
	}

	// worker value valudation
	init_workers, err := strconv.Atoi(cluster.Init_workers)
	if err != nil {
		response := Response{"Init_workers not a number"}
		js, err := json.Marshal(response)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		http.Error(w, string(js), 400)
		return
	}
	if init_workers < 0 || init_workers > 10 {
		response := Response{"Init_workers valid range: 0-10"}
		js, err := json.Marshal(response)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		http.Error(w, string(js), 400)
		return
	}
	if init_workers > 0 {
		if !regexpSize.MatchString(cluster.Worker_vm_ram) {
			response := Response{"The workers_vm_ram should be valid form, 512m, 1g"}
			js, err := json.Marshal(response)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			http.Error(w, string(js), 400)
			return
		}
		if !regexpSize.MatchString(cluster.Worker_vm_imgsize) {
			response := Response{"The worker_vm_imgsize should be valid form, 2g, 30g"}
			js, err := json.Marshal(response)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			http.Error(w, string(js), 400)
			return
		}
	}

	// pv_enable value validation
	pv_enable, err := strconv.Atoi(cluster.Pv_enable)
	if err != nil {
		response := Response{"Pv_enable not a number"}
		js, err := json.Marshal(response)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		http.Error(w, string(js), 400)
		return
	}
	if pv_enable < 0 || pv_enable > 1 {
		response := Response{"Pv_enable valid values: 0 or 1"}
		js, err := json.Marshal(response)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		http.Error(w, string(js), 400)
		return
	}

	// pv_enable value validation
	kubelet_master, err := strconv.Atoi(cluster.Kubelet_master)
	if err != nil {
		response := Response{"Kubelet_master not a number"}
		js, err := json.Marshal(response)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		http.Error(w, string(js), 400)
		return
	}
	if kubelet_master < 0 || kubelet_master > 1 {
		response := Response{"Kubelet_master valid values: 0 or 1"}
		js, err := json.Marshal(response)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		http.Error(w, string(js), 400)
		return
	}

	cluster.K8s_name = instanceid
	val := reflect.ValueOf(cluster)

	var jconf_param string
	var str strings.Builder

	// of course we can use marshal here instead of string concatenation, 
	// but now this is too simple case/data without any processing
	str.WriteString("{\"Command\":\"")
	str.WriteString(runscript)
	str.WriteString("\",\"CommandArgs\":{\"mode\":\"init\",\"k8s_name\":\"")
	str.WriteString(instanceid)
	str.WriteString("\"")

	for i := 0; i < val.NumField(); i++ {
		valueField := val.Field(i)

		typeField := val.Type().Field(i)
		tag := typeField.Tag

		tmpval := fmt.Sprintf("%s",valueField.Interface())

		if len(tmpval) == 0 {
			continue
		}

		fmt.Printf("[%s]",valueField);

		jconf_param = strings.ToLower(typeField.Name)
		if strings.Compare(jconf_param,"jname") == 0 {
			continue
		}
		fmt.Printf("jconf: %s,\tField Name: %s,\t Field Value: %v,\t Tag Value: %s\n", jconf_param, typeField.Name, valueField.Interface(), tag.Get("tag_name"))
		buf := fmt.Sprintf(",\"%s\": \"%s\"", jconf_param, tmpval)
		str.WriteString(buf)
	}

	str.WriteString("}}");

	fmt.Printf("C: [%s]\n",str.String())
	go realInstanceCreate(str.String())

	return
}


func HandleClusterDestroy(w http.ResponseWriter, r *http.Request) {
	var instanceid string
	params := mux.Vars(r)
	instanceid = params["instanceid"]
	var regexpInstanceId = regexp.MustCompile(`^[aA-zZ_]([aA-zZ0-9_])*$`)

	w.Header().Set("Content-Type", "application/json")

	// check the name field is between 3 to 40 chars
	if len(instanceid) < 3 || len(instanceid) > 40 {
		http.Error(w, "The instance name must be between 3-40", 400)
		return
	}
	if !regexpInstanceId.MatchString(instanceid) {
		http.Error(w, "The instance name should be valid form, ^[aA-zZ_]([aA-zZ0-9_])*$", 400)
		return
	}

	// check for existance
	// todo: to API
	SqliteDBPath := fmt.Sprintf("%s/var/db/k8s/%s.sqlite", workdir,instanceid)
	if !fileExists(SqliteDBPath) {
		response := Response{"no found"}
		js, err := json.Marshal(response)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		http.Error(w, string(js), http.StatusNotFound)
		return
	}

	// of course we can use marshal here instead of string concatenation, 
	// but now this is too simple case/data without any processing
	var str strings.Builder
	str.WriteString("{\"Command\":\"")
	str.WriteString(runscript)
	str.WriteString("\",\"CommandArgs\":{\"mode\":\"destroy\",\"k8s_name\":\"")
	str.WriteString(instanceid)
	str.WriteString("\"")
	str.WriteString("}}");

	fmt.Printf("C: [%s]\n",str.String())
	go realInstanceCreate(str.String())

	response := Response{"destroy"}
	js, err := json.Marshal(response)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Error(w, string(js), 200)
	return
}
