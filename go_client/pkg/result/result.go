package result

import (
	jsoniter "github.com/json-iterator/go"
	"net/http"
	"sync"
)

var (
	anyPool = sync.Pool{New: func() any {
		return &Wrapper{}
	}}
)

type Result struct {
	Status  bool   `json:"status"`
	Message string `json:"message,omitempty"`
	Data    any    `json:"data,omitempty"`
}

type Wrapper struct {
	code   int
	result Result
}

func (t *Wrapper) reset() {
	t.code = 0
	t.result.Status = false
	t.result.Message = ""
	var emptyValue any
	t.result.Data = emptyValue
	return
}

func (t *Wrapper) write(status bool, w http.ResponseWriter) {
	t.result.Status = status

	//	write header
	w.WriteHeader(t.code)
	w.Header().Set("Content-Type", "application/json")

	// encode stream
	_ = jsoniter.ConfigFastest.NewEncoder(w).Encode(t.result)

	// release
	t.reset()
	anyPool.Put(t)

}

func (t *Wrapper) Message(message string) *Wrapper {
	t.result.Message = message
	return t
}

func (t *Wrapper) Data(data any) *Wrapper {
	t.result.Data = data
	return t
}

func (t *Wrapper) Ok(w http.ResponseWriter) {
	t.write(true, w)
}

func (t *Wrapper) Err(w http.ResponseWriter) {
	t.write(false, w)
}

func New(code int) *Wrapper {
	return &Wrapper{
		code:   code,
		result: Result{},
	}
}

func Any(code int) *Wrapper {
	r := anyPool.Get().(*Wrapper)
	r.result.Data = nil
	r.code = code
	return r
}
