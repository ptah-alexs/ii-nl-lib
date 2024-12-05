package iinllib

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"math/rand"
)

type NodeT struct {
	Url      string // ссылка на станцию (+)
	Name     string // имя станции (+)
	Masked   bool   // станция недоступна, не выводится в список nodes.txt (+)
	AltPath  bool   // станция недоступна с этого узла, но доступна с других. инфа от чужих nodes.txt (+)
	Exclude  bool   // не обрабатывать nodes.txt этой станции
	LastEx   int64  // последняя успешная проверка в unix-time (+)
}

type NodesList struct {
	Lock      sync.RWMutex
	LockDepth int32
	ATime     int64
	Path      string
	Nodes     []NodeT
}

func NodeListNew(path string) *NodesList {
	var nl NodesList
	nl.Lock = sync.RWMutex{}
	fi, err := os.Stat(path)
	if err != nil {nl.ATime = 0} else {nl.ATime = fi.ModTime().Unix()}
	nl.Path = path
	nl.LockDepth = 0
	nl.LockF()
	nl.Nodes = *LoadNodes(path)
	nl.UnlockF()
	return &nl
}

func (r *NodesList) Write(path string) {
	r.LockF()
	defer r.UnlockF()
	fd, err := os.Create(path)
	if err != nil {
		fmt.Printf("Error open file")
		os.Exit(1)
	}
	buffer := bufio.NewWriter(fd)
	 for _, elem := range r.Nodes {
		 masked, altppath, exclude := "-", "-", "-"
		 if elem.Masked {masked = "+"}
		 if elem.AltPath {altppath = "+"}
		 if elem.Exclude {exclude = "+"}
		 _, err := buffer.WriteString(fmt.Sprintf("%s\t%s\t%d\t%s\t%s\t%s\n", elem.Url, elem.Name, elem.LastEx, masked, altppath, exclude))
		if err != nil {
			fmt.Printf("Error write file")
			os.Exit(1)
		}
	}
	if err := buffer.Flush(); err != nil {
		fmt.Printf("Error write file")
		os.Exit(1)
	}
}

func (r *NodesList) Update() *[]NodeT {
	at := int64(0)
	r.Lock.Lock()
	defer r.Lock.Unlock()
	fi, err := os.Stat(r.Path)
	if err == nil {at = fi.ModTime().Unix()}
	if at != r.ATime {
		r.LockF()
		r.Nodes = *LoadNodes(r.Path)
		r.UnlockF()
		r.ATime = at
	}
	return &r.Nodes
}

func (r *NodesList) LockF() bool {
	if atomic.AddInt32(&r.LockDepth, 1) > 1 {
		return true
	}
	try := 16
	for try > 0 {
		if err := os.Mkdir(r.LockPath(), 0777); err == nil {
			return true
		}
		time.Sleep(time.Second)
		try -= 1
	}
	fmt.Printf("Can not acquire lock for 16 seconds: %s", r.LockPath())
	return false
}

func (r *NodesList) UnlockF() {
	if atomic.AddInt32(&r.LockDepth, -1) > 0 {
		return
	}
	os.Remove(r.LockPath())
}

func (r *NodesList) LockPath() string {
	pat := strings.Replace(r.Path, "/", "_", -1)
	return fmt.Sprintf("%s/%s-file.lock", os.TempDir(), pat)
}

func (r *NodesList) Add(sheme, url, name string) int{
	trail := "/"
	if !(sheme == "http" || sheme == "https") {return 2}
	turl := strings.TrimSuffix(url,"/")
	if strings.Contains(turl,"?") {trail = ""}
	requrl := fmt.Sprintf("%s://%s%s", sheme, turl, trail)
	rr := r.Update()
	inx := -1
	for indx, v := range(*rr) {
		if v.Url == requrl {
			if v.Masked  && ! v.Exclude {
				inx = indx
			} else {return 1}
		}
	}
	if CheckII(requrl) {
		r.Lock.Lock()
		if inx != -1 {
			r.Nodes[inx].Masked = false
		} else {
			r.Nodes = append(r.Nodes, NodeT{Url: requrl, Name: name, LastEx: time.Now().Unix(), Masked: false, AltPath: false, Exclude: false})
		}
		r.Write(r.Path)
		r.Lock.Unlock()
		return 0
	} else {return 2}
}

func Generate(data *[]NodeT) string {
	var sb strings.Builder
	for _, v := range(*data) {
		altpath := "-"
		if v.AltPath {altpath = "+"}
		if !v.Masked || v.AltPath {
			sb.WriteString(fmt.Sprintf("%s\t%s\t%d\t%s\n", v.Url, v.Name, v.LastEx, altpath))
		}
	}
	return sb.String()
}

func Parse(data string) NodeT {
	ns := NodeT{Url: "", Name: "", Masked: false, AltPath: false, LastEx: 0, Exclude: false}
	aa := strings.Split(data, "\t")
	et := []string{"", "", "0"}
	for idx, val := range(aa) {
		et[idx] = val
		if idx == 2 {break}
	}
	ns.Url = et[0]
	ns.Name = et[1]
	fmt.Sscan(et[2], &ns.LastEx)
	return ns
}

func LoadNodes(path string) *[]NodeT {
	nl := []NodeT{}
	fd, err := os.Open(path)
	if err != nil {
		return &[]NodeT{}
	}
	defer fd.Close()
	scanner := bufio.NewScanner(fd)
	for scanner.Scan() {
		if scanner.Text() == "" {continue}
		aa := strings.Split(scanner.Text(), "\t")
		if len(aa) < 6 {continue}
		ns := NodeT{Url: "", Name: "", Masked: false, AltPath: false, LastEx: 0, Exclude: false}
		et := []string{"", "", "0", "-", "-", "-"}
		for idx, val := range(aa) {
			et[idx] = val
			if idx == 5 {break}
		}
		ns.Url = et[0]
		ns.Name = et[1]
		fmt.Sscan(et[2], &ns.LastEx)
		if et[3] == "+" {ns.Masked = true} else {ns.Masked = false}
		if et[4] == "+" {ns.AltPath = true} else {ns.AltPath = false}
		if et[5] == "+" {ns.Exclude = true} else {ns.Exclude = false}
		nl = append(nl, ns)
	}
	return &nl
}

func OpenNL(path string) *NodesList {
	nl := NodeListNew(path)
	if nl == nil {
		fmt.Printf("Can no open nodelst: %s\n", path)
		os.Exit(1)
	}
	return nl
}

func Getre(url string, numb int64) (int, []string) {
	resp, err := http.Get(url)
	if err != nil {
		return -1, []string{}
	}
	defer resp.Body.Close()
	rc := resp.StatusCode
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return -1, []string{}
	}
	vv := []string{}
	buf, _ := io.ReadAll(io.LimitReader(bytes.NewReader(body), numb))
	r := strings.NewReader(string(buf))
	bb := bufio.NewScanner(r)
	for bb.Scan() {
		vv = append(vv, bb.Text())
	}
	return rc, vv
}

func CheckII(url string) bool {
	urlt := strings.TrimSuffix(url, "/")
	code, answ := Getre(fmt.Sprintf("%s/list.txt", urlt), 511)
	if code != 200 {return false}
	rc := false
	nap := 4
	if jk:= len(answ); jk > 0 {
		noe := ""
		if jk < nap { nap = jk}
		rn := rand.Intn(nap)
		if  strings.Contains(answ[rn], ":") {
			noe = strings.SplitN(answ[rn], ":", 2)[0]
		}
		code1, answ1 := Getre(fmt.Sprintf("%s/u/e/%s/", urlt, noe), 511)
		if code1 != 200 {return false}
		mid := ""
		switch la := len(answ1); {
			case la == 1:
				if strings.Contains(answ1[0], ".") {rc = true}
			case la == 2:
				if answ1[1] != "" {
					mid = answ1[1]
				} else	if strings.Contains(answ1[0], ".") {rc = true}
			case la > 2:
				for {
					rnl := rand.Intn(la)
					if !strings.Contains(answ1[rnl], ".") {
						mid = answ1[rnl]
						break
					} else {continue}
				}
		}
		if mid != "" {
			code2, answ2 := Getre(fmt.Sprintf("%s/u/m/%s/", urlt, mid), 511)
			if code2 != 200 {return false}
			if len(answ2) > 0 {
				if strings.HasPrefix(answ2[0], fmt.Sprintf("%s:", mid)) {rc = true}
			}
		}
		return rc
	} else {return false}
}
