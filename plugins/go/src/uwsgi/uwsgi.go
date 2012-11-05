/*
	uWSGI go integration package
*/

package uwsgi

/*
#include <uwsgi.h>
extern struct uwsgi_server uwsgi;

// commodity functions to simulate argc/argv

static char ** uwsgi_go_helper_create_argv(int len) {
        return uwsgi_calloc(sizeof(char *) * len);
}

static void uwsgi_go_helper_set_argv(char **argv, int pos, char *item) {
        argv[pos] = item;
}

*/
import "C"

import (
	"os"
	"net/http"
	"net/http/cgi"
	"unsafe"
	"strings"
	"strconv"
	"io"
)

/*
This is the interface exposed to applications.
You are free to use it or simply rely on http.DefaultServeMux
*/
type AppInterface interface {
	// run this method on server startup
	Banner()
	// run after each fork()
	PostFork()
	// run after having initialized go engine
	PostInit()
	// run at each request
	RequestHandler(http.ResponseWriter, *http.Request)
}

// global instances...
var uwsgi_instance AppInterface
// this stores the modifier used by the go plugin (default 11)
var uwsgi_modifier1 int = -1;
// the following to objects are used to implement a sort of GC to avoid request environ and
// signal handlers to be garbage collected
var uwsgi_env_gc = make(map[*C.struct_wsgi_request](*map[string]string))
var uwsgi_signals_gc = make([]*func(int), 256)

// a struct implementing the AppInterface interface
type App struct {
}

func (app *App) Banner() {}
func (app *App) PostFork() {}
func (app *App) PostInit() {}
// here happens the magic, supporting http.DefaultServeMux
func (app *App) RequestHandler(w http.ResponseWriter, r *http.Request) {
	http.DefaultServeMux.ServeHTTP(w, r)
}

/*

	uWSGI api functions

*/

// raise a uWSGI signal
func (app *App) Signal(signum int) {
	C.uwsgi_signal_send(C.uwsgi.signal_socket, C.uint8_t(signum))
}

// set a user lock
func (app *App) Lock(num int) {
	C.uwsgi_user_lock(C.int(num));
}

// unset a user lock
func (app *App) Unlock(num int) {
	C.uwsgi_user_unlock(C.int(num));
}

// add a timer
func (app *App) AddTimer(signum int, seconds int) bool {
	if int(C.uwsgi_add_timer(C.uint8_t(signum), C.int(seconds))) == 0 {
		return true
	}
	return false
}

// add a red black timer
func (app *App) AddRbTimer(signum int, seconds int) bool {
	if int(C.uwsgi_signal_add_rb_timer(C.uint8_t(signum), C.int(seconds), C.int(0))) == 0 {
		return true
	}
	return false
}

// check if a signal is registered
func (app *App) SignalRegistered(signum int) bool {
	if int(C.uwsgi_signal_registered(C.uint8_t(signum))) == 0 {
		return false
	}
	return true
}

// register a signal
func (app *App) RegisterSignal(signum int, who string, handler func(int)) bool {
	if uwsgi_modifier1 == -1 {
		c_go := C.CString("go")
		defer C.free(unsafe.Pointer(c_go))
		uwsgi_modifier1 = int(C.uwsgi_plugin_modifier1(c_go))
		if uwsgi_modifier1 == -1 {
			return false
		}
	}
	c_who := C.CString(who)
	defer C.free(unsafe.Pointer(c_who))
	if int(C.uwsgi_register_signal(C.uint8_t(signum), c_who, unsafe.Pointer(&handler), C.uint8_t(uwsgi_modifier1))) == 0 {
		uwsgi_signals_gc[signum] = &handler
		return true
	}
	return false
}

// get an item from the cache
func (app *App) CacheGet(key string) []byte {
	if int(C.uwsgi_cache_enabled()) == 0 {
                return nil
        }

	k := C.CString(key)
        defer C.free(unsafe.Pointer(k))
        kl := len(key)
	var vl C.uint64_t = C.uint64_t(0)

	C.uwsgi_cache_rlock()

	c_value := C.uwsgi_cache_get(k, C.uint16_t(kl), &vl)

	var p []byte

	if vl > 0 {
		p = C.GoBytes((unsafe.Pointer)(c_value), C.int(vl))
	} else {
		p = nil
	}

	C.uwsgi_cache_rwunlock()

	return p
}

// remove an intem from the cache
func (app *App) CacheDel(key string) bool {
	if int(C.uwsgi_cache_enabled()) == 0 {
		return false
	}

	k := C.CString(key)
	defer C.free(unsafe.Pointer(k))
	kl := len(key)

	C.uwsgi_cache_wlock()

	if int(C.uwsgi_cache_del(k, C.uint16_t(kl), C.uint64_t(0))) < 0 {
		C.uwsgi_cache_rwunlock();
                return false;
	}

        C.uwsgi_cache_rwunlock();
	return true
}

// check if an item exists in the cache
func (app *App) CacheExists(key string) bool {
	if int(C.uwsgi_cache_enabled()) == 0 {
                return false
        }

        k := C.CString(key)
        defer C.free(unsafe.Pointer(k))
        kl := len(key)

        C.uwsgi_cache_rlock()

        if int(C.uwsgi_cache_exists(k, C.uint16_t(kl))) > 0 {
                C.uwsgi_cache_rwunlock();
                return true;
        }

        C.uwsgi_cache_rwunlock();
        return false
}

// put an item in the cache
func (app *App) CacheSetFlags(key string, p []byte, expires uint64, flags int) bool {

	if int(C.uwsgi_cache_enabled()) == 0 {
		return false
	}

	k := C.CString(key)
	defer C.free(unsafe.Pointer(k))
	kl := len(key)
	v := unsafe.Pointer(&p[0])
	vl := len(p)

	C.uwsgi_cache_wlock()

        if int(C.uwsgi_cache_set(k, C.uint16_t(kl), (*C.char)(v), C.uint64_t(vl), C.uint64_t(expires), C.uint16_t(flags))) < 0 {
                C.uwsgi_cache_rwunlock();
                return false;
        }

        C.uwsgi_cache_rwunlock();
	return true
}

func (app *App) CacheSet(key string, p []byte, expires uint64) bool {
	return app.CacheSetFlags(key, p, expires, 0);
}

func (app *App) CacheUpdate(key string, p []byte, expires uint64) bool {
	return app.CacheSetFlags(key, p, expires, 2);
}

// get the current worker id
func (app *App) WorkerId() int {
        return int(C.uwsgi.mywid)
}

// get the current mule id
func (app *App) MuleId() int {
        return int(C.uwsgi.muleid)
}

// get the current logsize (if available)
func (app *App) LogSize() int64 {
        return int64(C.uwsgi.shared.logsize)
}

/*

	C -> go and go -> C bridges

*/

//export uwsgi_go_helper_post_fork
func uwsgi_go_helper_post_fork() {
	uwsgi_instance.PostFork()
}

//export uwsgi_go_helper_post_init
func uwsgi_go_helper_post_init() {
	uwsgi_instance.PostInit()
}

//export uwsgi_go_helper_env_new
func uwsgi_go_helper_env_new(wsgi_req *C.struct_wsgi_request) *map[string]string {
	var env map[string]string
	env = make(map[string]string)
	// track env to avoid it being garbage collected...
	uwsgi_env_gc[wsgi_req] = &env
	return &env
}

//export uwsgi_go_helper_env_add
func uwsgi_go_helper_env_add(env *map[string]string, k *C.char, kl C.int, v *C.char, vl C.int) {
	var mk string = C.GoStringN(k, kl)
	var mv string = C.GoStringN(v, vl)
	(*env)[mk] = mv
}

/*

	http.* implementations

*/

type ResponseWriter struct {
	r	*http.Request
	wsgi_req *C.struct_wsgi_request
	headers      http.Header
	wroteHeader bool
	headers_chunk string
}

func (w *ResponseWriter) Write(p []byte) (n int, err error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}

	m := len(p)
	C.uwsgi_simple_response_write(w.wsgi_req, (*C.char)(unsafe.Pointer(&p[0])), C.size_t(m))
	return m+n, err
}

func (w *ResponseWriter) WriteHeader(status int) {
	proto := "HTTP/1.0"
	if w.r.ProtoAtLeast(1, 1) {
		proto = "HTTP/1.1"
	}
	codestring := http.StatusText(status)
	w.headers_chunk += proto + " " + strconv.Itoa(status) + " " + codestring + "\r\n"
	C.uwsgi_simple_set_status(w.wsgi_req, C.int(status))
	if w.headers.Get("Content-Type") == "" {
		w.headers.Set("Content-Type", "text/html; charset=utf-8")
	}
	for k := range w.headers {
		for _, v := range w.headers[k] {
			v = strings.NewReplacer("\n", " ", "\r", " ").Replace(v)
			v = strings.TrimSpace(v)
			w.headers_chunk += k + ": " + v + "\r\n"
			C.uwsgi_simple_inc_headers(w.wsgi_req)
		}
	}
	w.headers_chunk += "\r\n"
	c_h_chunk := C.CString(w.headers_chunk)
	defer C.free(unsafe.Pointer(c_h_chunk))
	C.uwsgi_simple_response_write_header(w.wsgi_req, c_h_chunk, C.size_t(len(w.headers_chunk)))
	w.wroteHeader = true
}

func (w *ResponseWriter) Header() http.Header {
	return w.headers
}


type BodyReader struct {
	wsgi_req *C.struct_wsgi_request
}

// there is no close in request body
func (br *BodyReader) Close() error {
	return nil
}

func (br *BodyReader) Read(p []byte) (n int, err error) {
	m := len(p)
	rlen := int(C.uwsgi_simple_request_read(br.wsgi_req, (*C.char)(unsafe.Pointer(&p[0])), C.size_t(m)))
	if rlen < 0 {
		err = io.ErrUnexpectedEOF
		rlen = 0
	} else if rlen == 0 {
		err = io.EOF
	}
	return rlen, err
}

//export uwsgi_go_helper_request
func uwsgi_go_helper_request(env *map[string]string, wsgi_req *C.struct_wsgi_request) {
	httpReq, err := cgi.RequestFromMap(*env)
	if err != nil {
	} else {
		httpReq.Body = &BodyReader{wsgi_req}
		w := ResponseWriter{httpReq, wsgi_req,http.Header{},false, ""}
		uwsgi_instance.RequestHandler(&w, httpReq)
	}
}

//export uwsgi_go_helper_signal_handler
func uwsgi_go_helper_signal_handler(signum int, handler *func(int)) int {
	(*handler)(signum)
	return 0;
}

//export uwsgi_go_helper_run_core
func uwsgi_go_helper_run_core(core_id int) {
	go C.simple_loop_run_int(C.int(core_id))
}

/*
	the main function, running the uWSGI server via libuwsgi.so
*/
func Run(u AppInterface) {
	uwsgi_instance = u
        argc := len(os.Args)
        argv := C.uwsgi_go_helper_create_argv(C.int(argc))
        for i, s := range os.Args {
                C.uwsgi_go_helper_set_argv(argv, C.int(i), C.CString(s))
        }
	// just a funny banner...
	u.Banner()
        C.uwsgi_init(C.int(argc), argv, nil)
}
