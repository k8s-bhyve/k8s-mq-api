// CBSD Project 2013-2022
// K8s-bhyve project 2020-2022
// Simple demo/sample for CBSD K8S API
package main

import (
	"bufio"
	"encoding/json"
	"crypto/md5"
	"github.com/gorilla/mux"
	"log"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"fmt"
	"io/ioutil"
	"reflect"
	"flag"
	"time"

	"golang.org/x/crypto/ssh"
)

var lock = sync.RWMutex{}
var config Config
var runscript string
var workdir string
var server_url string
var acl_enable bool

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
	Pv_size			string	`"pv_size,omitempty"`
	Kubelet_master		string	`"kubelet_master,omitempty"`
	Email			string	`"email,omitempty"`
	Callback		string	`"callback,omitempty"`
	Pubkey			string	`"pubkey,omitempty"`
	Recomendation		string	`"recomendation,omitempty"`
}
// Todo: validate mod?
//  e.g for simple check:
//  K8s_name  string `json:"name" validate:"required,min=2,max=100"`

var (
	body		= flag.String("body", "", "Body of message")
	cbsdEnv		= flag.String("cbsdenv", "/usr/jails", "CBSD workdir environment")
	configFile	= flag.String("config", "/usr/local/etc/cbsd-mq-k8s.json", "Path to config.json")
	runScript	= flag.String("runscript", "k8s", "CBSD K8S target run script")
	listen *string	= flag.String("listen", "127.0.0.1:8081", "Listen host:port")
	allowListFile	= flag.String("allowlist", "", "Path to PubKey whitelist, e.g: -allowlist /usr/local/etc/cbsd-mq-api.allow")
	dbDir		= flag.String("dbdir", "/var/db/cbsd-k8s", "db root dir")
	serverUrl	= flag.String("server_url", "http://127.0.0.1:65532", "Server URL for external requests")
)

type AllowList struct {
	keyType string
	key     string
	comment string
	cid     string
	next    *AllowList // link to the next records
}

// linked struct
type Feed struct {
	length int
	start  *AllowList
}

type MyFeeds struct {
	f *Feed
}

func (f *Feed) Append(newAllow *AllowList) {
	if f.length == 0 {
		f.start = newAllow
	} else {
		currentPost := f.start
		for currentPost.next != nil {
			currentPost = currentPost.next
		}
		currentPost.next = newAllow
	}
	f.length++
}

func newAllow(keyType string, key string, comment string) *AllowList {

	KeyInList := fmt.Sprintf("%s %s %s", keyType, key, comment)
	uid := []byte(KeyInList)
	cid := md5.Sum(uid)

	cidString := fmt.Sprintf("%x", cid)

	np := AllowList{keyType: keyType, key: key, comment: comment, cid: cidString}
	//      np.Response = ""
	//      np.Time = 0
	return &np
}

// we need overwrite Content-Type here
// https://stackoverflow.com/questions/59763852/can-you-return-json-in-golang-http-error
func JSONError(w http.ResponseWriter, message string, code int) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// write header is mandatory to overwrite header
	w.WriteHeader(code)

	if len(message) > 0 {
		response := Response{message}
		js, err := json.Marshal(response)
		if err != nil {
			fmt.Fprintln(w, "{\"Message\":\"Marshal error\"}", http.StatusInternalServerError)
			return
		}
		http.Error(w, string(js), code)
	} else {
		http.Error(w, "{}", http.StatusNotFound)
	}
	return
}

func fileExists(filename string) bool {
	_, err := os.Stat(filename)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println("file does not exist", filename)
			return false
		} else {
			// error
			return false
		}
	} else {
		// file exist
		return true
	}
}
// main function to boot up everything
func main() {

	flag.Parse()
	var err error

	config, err = LoadConfiguration(*configFile)

	runscript = *runScript
	workdir=config.CbsdEnv
	server_url = config.ServerUrl

	if err != nil {
		fmt.Println("config load error")
		os.Exit(1)
	}

	if !fileExists(config.Recomendation) {
		fmt.Printf("no such Recomendation script, please check config/path: %s\n", config.Recomendation)
		os.Exit(1)
	}
	if !fileExists(config.Freejname) {
		fmt.Printf("no such Freejname script, please check config/path: %s\n", config.Freejname)
		os.Exit(1)
	}

	if !fileExists(*dbDir) {
		fmt.Printf("* db dir created: %s\n", *dbDir)
		os.MkdirAll(*dbDir, 0770)
	}

	f := &Feed{}

	// WhiteList
	if (*allowListFile == "") || (!fileExists(*allowListFile)) {
		fmt.Println("* no such allowList file ( -allowlist <path> )")
		fmt.Println("* ACL disabled: fully open system, all queries are permit!")
		acl_enable = false
	} else {
		fmt.Printf("* ACL enabled: %s\n", *allowListFile)
		acl_enable = true
		// loadconfig
		fd, err := os.Open(*allowListFile)
		if err != nil {
			panic(err)
		}
		defer fd.Close()

		scanner := bufio.NewScanner(fd)

		var keyType string
		var key string
		var comment string

		scanner.Split(bufio.ScanLines)
		var txtlines []string

		for scanner.Scan() {
			txtlines = append(txtlines, scanner.Text())
		}

		fd.Close()

		for _, eachline := range txtlines {
			fmt.Println(eachline)
			// todo: input validation
			// todo: auto-reload, signal
			_, err := fmt.Sscanf(eachline, "%s %s %s", &keyType, &key, &comment)
			if err != nil {
				log.Fatal(err)
				break
			}
			fmt.Printf("* ACL loaded: [%s %s %s]\n", keyType, key, comment)
			p := newAllow(keyType, key, comment)
			f.Append(p)
		}
		fmt.Printf("* AllowList Length: %v\n", f.length)
	}

	// setup: we need to pass Feed into handler function
	feeds := &MyFeeds{f: f}

	router := mux.NewRouter()
	router.HandleFunc("/api/v1/create/{InstanceId}", feeds.HandleClusterCreate).Methods("POST")
	router.HandleFunc("/api/v1/status/{InstanceId}", feeds.HandleClusterStatus).Methods("GET")
	router.HandleFunc("/api/v1/kubeconfig/{InstanceId}", feeds.HandleClusterKubeConfig).Methods("GET")
	router.HandleFunc("/api/v1/destroy/{InstanceId}", feeds.HandleClusterDestroy).Methods("GET")
	router.HandleFunc("/api/v1/cluster", feeds.HandleClusterCluster).Methods("GET")

	fmt.Println("* Listen",*listen)
	fmt.Println("* Server URL", server_url)
	log.Fatal(http.ListenAndServe(*listen, router))
}

func validateCid(Cid string) bool {
	var regexpCid = regexp.MustCompile("^[a-f0-9]{32}$")

	if regexpCid.MatchString(Cid) {
		return true
	} else {
		return false
	}
}

func validateInstanceId(InstanceId string) bool {
	var regexpInstanceId = regexp.MustCompile("^[a-z_]([a-z0-9_])*$")

	if len(InstanceId) < 1 || len(InstanceId) > 40 {
		return false
	}

	if regexpInstanceId.MatchString(InstanceId) {
		return true
	} else {
		return false
	}
}

func isPubKeyAllowed(feeds *MyFeeds, PubKey string) bool {
	//ALLOWED?
	var p *AllowList
	currentAllow := feeds.f.start

	if !acl_enable {
		return true
	}

	for i := 0; i < feeds.f.length; i++ {
		p = currentAllow
		currentAllow = currentAllow.next
		ResultKeyType := (string(p.keyType))
		ResultKey := (string(p.key))
		ResultKeyComment := (string(p.comment))
		//fmt.Println("ResultType: ", ResultKeyType)
		KeyInList := fmt.Sprintf("%s %s %s", ResultKeyType, ResultKey, ResultKeyComment)
		fmt.Printf("[%s][%s]\n", PubKey, KeyInList)

		if len(PubKey) == len(KeyInList) {
			if strings.Compare(PubKey, KeyInList) == 0 {
				fmt.Printf("pubkey matched\n")
				return true
			}
		}
	}

	return false
}

func isCidAllowed(feeds *MyFeeds, Cid string) bool {
	//ALLOWED?
	var p *AllowList
	currentAllow := feeds.f.start

	if !acl_enable {
		return true
	}

	for i := 0; i < feeds.f.length; i++ {
		p = currentAllow
		currentAllow = currentAllow.next
		CidInList := (string(p.cid))
		if strings.Compare(Cid, CidInList) == 0 {
			fmt.Printf("Cid ACL matched: %s\n", Cid)
			return true
		}
	}

	return false
}

func (feeds *MyFeeds) HandleClusterStatus(w http.ResponseWriter, r *http.Request) {
	var InstanceId string
	params := mux.Vars(r)

	InstanceId = params["InstanceId"]
	if !validateInstanceId(InstanceId) {
		JSONError(w, "The InstanceId should be valid form: ^[a-z_]([a-z0-9_])*$ (maxlen: 40)", http.StatusNotFound)
		return
	}

	Cid := r.Header.Get("cid")
	if !validateCid(Cid) {
		JSONError(w, "The cid should be valid form: ^[a-f0-9]{32}$", http.StatusNotFound)
		return
	}

	if !isCidAllowed(feeds, Cid) {
		fmt.Printf("CID not in ACL: %s\n", Cid)
		JSONError(w, "not allowed", http.StatusInternalServerError)
		return
	}

	HomePath := fmt.Sprintf("%s/%s/vms", *dbDir, Cid)
	if _, err := os.Stat(HomePath); os.IsNotExist(err) {
		JSONError(w, "not found", http.StatusNotFound)
		return
	}

	mapfile := fmt.Sprintf("%s/var/db/k8s/map/%s-%s", workdir, Cid, InstanceId)

	if !fileExists(config.Recomendation) {
		fmt.Printf("no such map file %s/var/db/k8s/map/%s-%s\n", workdir, Cid, InstanceId)
		JSONError(w, "not found", http.StatusNotFound)
		return
	}

	b, err := ioutil.ReadFile(mapfile) // just pass the file name
	if err != nil {
		fmt.Printf("unable to read jname from %s/var/db/k8s/map/%s-%s\n", workdir, Cid, InstanceId)
		JSONError(w, "not found", http.StatusNotFound)
		return
	}

	SqliteDBPath := fmt.Sprintf("%s/%s/%s-bhyve.ssh", *dbDir, Cid, string(b))
	if fileExists(SqliteDBPath) {
		b, err := ioutil.ReadFile(SqliteDBPath) // just pass the file name
		if err != nil {
			JSONError(w, "", 400)
			return
		} else {
			// already in json - send as-is
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.WriteHeader(200)
			http.Error(w, string(b), 200)
			return
		}
	} else {
		JSONError(w, "", http.StatusNotFound)
	}
}

func (feeds *MyFeeds) HandleClusterKubeConfig(w http.ResponseWriter, r *http.Request) {
	var InstanceId string
	params := mux.Vars(r)

	InstanceId = params["InstanceId"]
	if !validateInstanceId(InstanceId) {
		JSONError(w, "The InstanceId should be valid form: ^[a-z_]([a-z0-9_])*$ (maxlen: 40)", http.StatusNotFound)
		return
	}

	Cid := r.Header.Get("cid")
	if !validateCid(Cid) {
		JSONError(w, "The cid should be valid form: ^[a-f0-9]{32}$", http.StatusNotFound)
		return
	}

	if !isCidAllowed(feeds, Cid) {
		fmt.Printf("CID not in ACL: %s\n", Cid)
		JSONError(w, "not allowed", http.StatusInternalServerError)
		return
	}

	VmPath := fmt.Sprintf("%s/%s/cluster-%s", *dbDir, Cid, InstanceId)

	if !fileExists(VmPath) {
		fmt.Printf("ClusterKubeConfig: Error read vmpath file  [%s]\n", VmPath)
		JSONError(w, "", 400)
		return
	}

	b, err := ioutil.ReadFile(VmPath) // just pass the file name
	if err != nil {
		fmt.Printf("Error read vmpath file  [%s]\n", VmPath)
		JSONError(w, "", 400)
		return
	} else {
		kubeFile := fmt.Sprintf("%s/var/db/k8s/%s.kubeconfig", workdir, string(b))
		if fileExists(kubeFile) {
			b, err := ioutil.ReadFile(kubeFile) // just pass the file name
			if err != nil {
				fmt.Printf("unable to read content %s\n", kubeFile)
				JSONError(w, "", http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "text/plain")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.WriteHeader(200)
			http.Error(w, string(b), 200)
			return
		} else {
			fmt.Printf("Error read kubeconfig  [%s]\n", kubeFile)
			JSONError(w, "", 400)
			return
		}
	}
}

// read /var/db/cbsd-k8s/<cid>/vms/vm.list
func (feeds *MyFeeds) HandleClusterCluster(w http.ResponseWriter, r *http.Request) {
	Cid := r.Header.Get("cid")
	if !validateCid(Cid) {
		JSONError(w, "The cid should be valid form: ^[a-f0-9]{32}$", http.StatusNotFound)
		return
	}

	if !isCidAllowed(feeds, Cid) {
		fmt.Printf("CID not in ACL: %s\n", Cid)
		JSONError(w, "not allowed", http.StatusInternalServerError)
		return
	}

	HomePath := fmt.Sprintf("%s/%s/vms", *dbDir, Cid)
	//fmt.Println("CID IS: [ %s ]", cid)
	if _, err := os.Stat(HomePath); os.IsNotExist(err) {
		JSONError(w, "", http.StatusNotFound)
		return
	}

	SqliteDBPath := fmt.Sprintf("%s/%s/vm.list", *dbDir, Cid)
	if fileExists(SqliteDBPath) {
		b, err := ioutil.ReadFile(SqliteDBPath) // just pass the file name
		if err != nil {
			JSONError(w, "", http.StatusNotFound)
			return
		} else {
			// already in json - send as-is
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.WriteHeader(200)
			http.Error(w, string(b), 200)
			return
		}
	} else {
		JSONError(w, "", http.StatusNotFound)
		return
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

func getNodeRecomendation(body string, offer string) {
	// offer - recomendation host from user, we can check them in external helper
	// for valid/resource

	var result string

	if len(offer) > 1 {
		result = offer
		fmt.Printf("FORCED Host Recomendation: [%s]\n", result)
	} else {
		cmdStr := fmt.Sprintf("%s %s", config.Recomendation, body)
		cmdArgs := strings.Fields(cmdStr)
		cmd := exec.Command(cmdArgs[0], cmdArgs[1:len(cmdArgs)]...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			fmt.Println("get recomendation script failed")
			return
		}
		result = (string(out))
	}

	fmt.Printf("Host Recomendation: [%s]\n", result)

	result = strings.Replace(result, ".", "_", -1)
	result = strings.Replace(result, "-", "_", -1)

	tube := fmt.Sprintf("cbsd_%s", result)
	reply := fmt.Sprintf("cbsd_%s_result_id", result)

	fmt.Printf("Tube selected: [%s]\n", tube)
	fmt.Printf("ReplyTube selected: [%s]\n", reply)

	config.BeanstalkConfig.Tube = tube
	config.BeanstalkConfig.ReplyTubePrefix = reply
}


func getJname() string {
	cmdStr := fmt.Sprintf("%s", config.Freejname)
	cmdArgs := strings.Fields(cmdStr)
	cmd := exec.Command(cmdArgs[0], cmdArgs[1:len(cmdArgs)]...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		fmt.Println("get freejname script failed")
		return ""
	}
	result := (string(out))
	fmt.Printf("Freejname Recomendation: [%s]\n", result)
	return result
}

func (feeds *MyFeeds) HandleClusterCreate(w http.ResponseWriter, r *http.Request) {
	var InstanceId string
	params := mux.Vars(r)

	InstanceId = params["InstanceId"]
	if !validateInstanceId(InstanceId) {
		JSONError(w, "The InstanceId should be valid form: ^[a-z_]([a-z0-9_])*$ (maxlen: 40)", http.StatusNotFound)
		return
	}

	var regexpSize = regexp.MustCompile(`^[1-9](([0-9]+)?)([m|g|t])$`)
	var regexpEmail = regexp.MustCompile("^[a-zA-Z0-9.!#$%&'*+/=?^_`{|}~-]+@[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?(?:\\.[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)*$")
	var regexpCallback = regexp.MustCompile(`^(http|https)://`)
	var regexpPubkey = regexp.MustCompile("^(ssh-rsa|ssh-dss|ssh-ed25519|ecdsa-[^ ]+) ([^ ]+) ?(.*)")
	var regexpParamName = regexp.MustCompile(`^[a-z_]+$`)
	var regexpParamVal = regexp.MustCompile(`^[aA-zZ0-9_\-. ]+$`)
	var regexpHostName = regexp.MustCompile(`^[aA-zZ0-9_\-\.]+$`)

	if r.Body == nil {
		JSONError(w, "please send a request body", http.StatusInternalServerError)
		return
	}

	fmt.Println("create wakeup")

	var suggest string
	var cluster Cluster
	_ = json.NewDecoder(r.Body).Decode(&cluster)

	if len(cluster.Pubkey) < 30 {
		fmt.Printf("Error: Pubkey too small\n")
		JSONError(w, "Pubkey too small", http.StatusInternalServerError)
		return
	}

	if len(cluster.Pubkey) > 1000 {
		fmt.Printf("Error: Pubkey too long\n")
		JSONError(w, "Pubkey too long", http.StatusInternalServerError)
		return
	}

	if !regexpPubkey.MatchString(cluster.Pubkey) {
		fmt.Printf("Error: pubkey should be valid form. valid key: ssh-rsa,ssh-ed25519,ecdsa-*,ssh-dsa XXXXX comment\n")
		JSONError(w, "pubkey should be valid form. valid key: ssh-rsa,ssh-ed25519,ecdsa-*,ssh-dsa XXXXX comment", http.StatusInternalServerError)
		return
	}

	parsedKey, _, _, _, err := ssh.ParseAuthorizedKey([]byte(cluster.Pubkey))
	if err != nil {
		fmt.Printf("Error: ParseAuthorizedKey\n")
		JSONError(w, "ParseAuthorizedKey", http.StatusInternalServerError)
		return
	}

	fmt.Printf("pubKey: [%x]\n", parsedKey)
	uid := []byte(cluster.Pubkey)

	//existance?
	// check for existance
	cid := md5.Sum(uid)

	if !isPubKeyAllowed(feeds, cluster.Pubkey) {
		fmt.Printf("Pubkey not in ACL: %s\n", cluster.Pubkey)
		JSONError(w, "not allowed", http.StatusInternalServerError)
		return
	}

	// Count+Limits per CID should be implemented here (database req).
	ClusterTimePath := fmt.Sprintf("%s/%x.time", *dbDir, cid)
	if fileExists(ClusterTimePath) {
		fmt.Printf("Error: limit of clusters per user has been exceeded: [%s]\n", ClusterTimePath)
		JSONError(w, "limit of clusters per user has been exceeded: 1", http.StatusInternalServerError)
		return
	}

	ClusterTime := time.Now().Unix()

	tfile, fileErr := os.Create(ClusterTimePath)
	if fileErr != nil {
		fmt.Println(fileErr)
		return
	}
	fmt.Fprintf(tfile, "%s\n%s\n", ClusterTime,InstanceId)

	tfile.Close()

	ClusterPathDir := fmt.Sprintf("%s/%x", *dbDir, cid)

	if !fileExists(ClusterPathDir) {
		os.Mkdir(ClusterPathDir, 0775)
	}

	ClusterPath := fmt.Sprintf("%s/%x/cluster-%s", *dbDir, cid, InstanceId)

	if fileExists(ClusterPath) {
		fmt.Printf("Error: cluster already exist: [%s]\n", ClusterPath)
		JSONError(w, "cluster already exist", http.StatusInternalServerError)
		return
	}

	fmt.Printf("cluster file not exist, create empty: [%s]\n", ClusterPath)
	// create empty file
	f, err := os.Create(ClusterPath)

	if err != nil {
		log.Fatal(err)
	}

/*
	// check for existance
	// todo: to API
	SqliteDBPath := fmt.Sprintf("%s/var/db/k8s/%s.sqlite", workdir,InstanceId)
	if fileExists(SqliteDBPath) {
		response := Response{"cluster already exist"}
		js, err := json.Marshal(response)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		http.Error(w, string(js), 400)
		return
	}
*/

	//json.NewEncoder(w).Encode(cluster)

	if len(cluster.Recomendation) > 1 {
		if !regexpHostName.MatchString(cluster.Recomendation) {
			fmt.Printf("Error: wrong hostname recomendation: [%s]\n", cluster.Recomendation)
			JSONError(w, "recomendation should be valid form. valid form", http.StatusInternalServerError)
			return
		} else {
			fmt.Printf("Found cluster recomendation: [%s]\n", cluster.Recomendation)
			suggest = cluster.Recomendation
		}
	} else {
		suggest = ""
	}

	if ( len(cluster.Email)>2 ) {
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
	}

	if ( len(cluster.Callback)>2) {
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
	}

	Jname := getJname()
	if len(Jname) < 1 {
		log.Fatal("unable to get jname")
		return
	}

	fmt.Printf("GET NEXT FREE JNAME: [%s]\n", Jname)

	_, err2 := f.WriteString(Jname)

	if err2 != nil {
		log.Fatal(err2)
	}

	f.Close()

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

	cluster.K8s_name = InstanceId
	val := reflect.ValueOf(cluster)

	var jconf_param string
	var str strings.Builder
	var recomendation strings.Builder

	// of course we can use marshal here instead of string concatenation, 
	// but now this is too simple case/data without any processing
	str.WriteString("{\"Command\":\"")
	str.WriteString(runscript)
	str.WriteString("\",\"CommandArgs\":{\"mode\":\"init\",\"k8s_name\":\"")
//	str.WriteString(InstanceId)
	str.WriteString(Jname)
	str.WriteString("\"")

	for i := 0; i < val.NumField(); i++ {
		valueField := val.Field(i)

		typeField := val.Type().Field(i)
		tag := typeField.Tag

		tmpval := fmt.Sprintf("%s",valueField.Interface())

		if len(tmpval) == 0 {
			continue
		}
		if len(tmpval) > 1000 {
			fmt.Printf("Error: param val too long\n")
			continue
		}

		fmt.Printf("[%s]",valueField);

		if len(typeField.Name) > 30 {
			fmt.Printf("Error: param name too long\n")
			continue
		}

		jconf_param = strings.ToLower(typeField.Name)

		if strings.Compare(jconf_param,"jname") == 0 {
			continue
		}

		if !regexpParamName.MatchString(jconf_param) {
			fmt.Printf("Error: wrong paramname: [%s]\n", jconf_param)
			continue
		} else {
			fmt.Printf("paramname test passed: [%s]\n", jconf_param)
		}

		// validate unknown data values
		switch jconf_param {
		case "type":
		case "imgsize":
		case "ram":
		case "cpus":
		case "pkglist":
		case "pubkey":
		case "host_hostname":
		case "init_masters":
		case "init_workers":
		case "master_vm_ram":
		case  "master_vm_cpus":
		case "master_vm_imgsize":
		case "worker_vm_ram":
		case "worker_vm_cpus":
		case "worker_vm_imgsize":
		case "pv_enable":
		case "kubelet_master":
		case "email":
		case "callback":

		default:
			if !regexpParamVal.MatchString(tmpval) {
				fmt.Printf("Error: wrong paramval for %s: [%s]\n", jconf_param, tmpval)
				continue
			}
		}

		fmt.Printf("jconf: %s,\tField Name: %s,\t Field Value: %v,\t Tag Value: %s\n", jconf_param, typeField.Name, valueField.Interface(), tag.Get("tag_name"))
		buf := fmt.Sprintf(",\"%s\": \"%s\"", jconf_param, tmpval)
		buf2 := fmt.Sprintf("%s ", tmpval)
		str.WriteString(buf)
		recomendation.WriteString(buf2)
	}

	str.WriteString("}}");
	fmt.Printf("C: [%s]\n",str.String())
	response := fmt.Sprintf("{ \"Message\": [\"curl -H cid:%x %s/api/v1/cluster\", \"curl -H cid:%x %s/api/v1/status/%s\", \"curl -H cid:%x %s/api/v1/kubeconfig/%s\",  \"curl -H cid:%x %s/api/v1/snapshot/%s\", \"curl -H cid:%x %s/api/v1/rollback/%s\", \"curl -H cid:%x %s/api/v1/destroy/%s\"] }", cid, server_url, cid, server_url, InstanceId, cid, server_url, InstanceId, cid, server_url, InstanceId, cid, server_url, InstanceId, cid, server_url, InstanceId)

	getNodeRecomendation(recomendation.String(), suggest)
	go realInstanceCreate(str.String())

	// !!! MKDIR
	ClusterMapDir := fmt.Sprintf("%s/var/db/k8s/map", workdir)

	if !fileExists(ClusterMapDir) {
		os.Mkdir(ClusterMapDir, 0775)
	}

	mapfile := fmt.Sprintf("%s/%x-%s", ClusterMapDir, cid, InstanceId)
	m, err := os.Create(mapfile)

	if err != nil {
		log.Fatal(err)
	}

	_, err3 := m.WriteString(Jname)

	if err3 != nil {
		log.Fatal(err3)
	}

	m.Close()

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// write header is mandatory to overwrite header
	w.WriteHeader(200)
	fmt.Fprintln(w, response)

	return
}


//func HandleClusterDestroy(w http.ResponseWriter, r *http.Request) {
func (feeds *MyFeeds) HandleClusterDestroy(w http.ResponseWriter, r *http.Request) {
	var InstanceId string
	params := mux.Vars(r)

	InstanceId = params["InstanceId"]

	if !validateInstanceId(InstanceId) {
		JSONError(w, "The InstanceId should be valid form: ^[a-z_]([a-z0-9_])*$ (maxlen: 40)", http.StatusNotFound)
		return
	}

	Cid := r.Header.Get("cid")
	if !validateCid(Cid) {
		JSONError(w, "The cid should be valid form: ^[a-f0-9]{32}$", http.StatusNotFound)
		return
	}

	if !isCidAllowed(feeds, Cid) {
		fmt.Printf("CID not in ACL: %s\n", Cid)
		JSONError(w, "not allowed", http.StatusInternalServerError)
		return
	}

	VmPath := fmt.Sprintf("%s/%s/cluster-%s", *dbDir, Cid, InstanceId)
	mapfile := fmt.Sprintf("%s/var/db/k8s/map/%s-%s", workdir, Cid, InstanceId)

	HomePath := fmt.Sprintf("%s/%s/vms", *dbDir, Cid)
	if _, err := os.Stat(HomePath); os.IsNotExist(err) {
		// orphaned cluster-<file> ? 
		if fileExists(VmPath) {
			e := os.Remove(VmPath)
			if e != nil {
				log.Fatal(e)
			}
		}
		if fileExists(mapfile) {
			e := os.Remove(mapfile)
			if e != nil {
				log.Fatal(e)
			}
		}
		fmt.Println("path not found:", HomePath)
		JSONError(w, "not found", http.StatusNotFound)
		return
	}


	if !fileExists(config.Recomendation) {
		fmt.Printf("no such map file %s/var/db/k8s/map/%s-%s\n", workdir, Cid, InstanceId)
		JSONError(w, "not found", http.StatusNotFound)
		return
	}

	b, err := ioutil.ReadFile(mapfile) // just pass the file name
	if err != nil {
		fmt.Printf("unable to read jname from %s/var/db/k8s/map/%s-%s\n", workdir, Cid, InstanceId)
		JSONError(w, "not found", http.StatusNotFound)
		return
	}

	fmt.Printf("Destroy %s via %s/var/db/k8s/map/%x-%s\n", string(b), workdir, Cid, InstanceId)

	// of course we can use marshal here instead of string concatenation, 
	// but now this is too simple case/data without any processing
	var str strings.Builder
	str.WriteString("{\"Command\":\"")
	str.WriteString(runscript)
	str.WriteString("\",\"CommandArgs\":{\"mode\":\"destroy\",\"k8s_name\":\"")
	str.WriteString(string(b))
	str.WriteString("\"")
	str.WriteString("}}");

	//get guest nodes & tubes
	SqliteDBPath := fmt.Sprintf("%s/%s/%s.node", *dbDir, Cid, string(b))
	if fileExists(SqliteDBPath) {
		b, err := ioutil.ReadFile(SqliteDBPath) // just pass the file name
		if err != nil {
			fmt.Printf("unable to read node map: %s\n", SqliteDBPath)
			JSONError(w, "unable to read node map", http.StatusNotFound)
			return
		} else {
			result := strings.Replace(string(b), ".", "_", -1)
			result = strings.Replace(result, "-", "_", -1)
			result = strings.TrimSuffix(result, "\n")
			tube := fmt.Sprintf("cbsd_%s", result)
			reply := fmt.Sprintf("cbsd_%s_result_id", result)
			// result: srv-03.olevole.ru
			config.BeanstalkConfig.Tube = tube
			config.BeanstalkConfig.ReplyTubePrefix = reply
		}
	} else {
		fmt.Printf("unable to read node map: %s\n", SqliteDBPath)
		JSONError(w, "unable to read node map", http.StatusNotFound)
		return
	}

	fmt.Printf("C: [%s]\n",str.String())
	go realInstanceCreate(str.String())

	e := os.Remove(mapfile)
	if e != nil {
		log.Fatal(e)
	}

	// remove from FS
	if fileExists(VmPath) {
		b, err := ioutil.ReadFile(VmPath) // just pass the file name
		if err != nil {
			fmt.Printf("Error read UID from  [%s]\n", string(b))
		} else {

			fmt.Printf("   REMOVE: %s\n", VmPath)
			e = os.Remove(VmPath)

			VmPath = fmt.Sprintf("%s/%s/%s.node", *dbDir, Cid, string(b))
			fmt.Printf("   REMOVE: %s\n", VmPath)
			e = os.Remove(VmPath)

			VmPath = fmt.Sprintf("%s/%s/%s-bhyve.ssh", *dbDir, Cid, string(b))
			fmt.Printf("   REMOVE: %s\n", VmPath)
			e = os.Remove(VmPath)

			VmPath = fmt.Sprintf("%s/%s/vms/%s", *dbDir, Cid, string(b))
			fmt.Printf("   REMOVE: %s\n", VmPath)
			e = os.Remove(VmPath)
		}
	}

	JSONError(w, "destroy", 200)
	return
}
