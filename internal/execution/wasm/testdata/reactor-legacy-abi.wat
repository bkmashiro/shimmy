;; reactor-legacy-abi.wat — tiny python-reactor ABI compatibility fixture.
;;
;; This is not CPython. It only models the old host-facing reactor ABI:
;;   py_init()
;;   py_exec(ptr, len)
;;   alloc(size) -> ptr
;;   dealloc(ptr)
;;   resp_buf() -> ptr
;;   resp_len() -> ptr
;;
;; It intentionally does NOT export evaluate. Tests use it to prove the host
;; still falls back to py_exec + resp_buf/resp_len for pre-evaluate reactor
;; artifacts.
(module
  (memory (export "memory") 1)
  (data (i32.const 0) "\04\00\00\00")

  (func (export "_initialize"))
  (func (export "py_init"))

  (func (export "alloc") (param $size i32) (result i32)
    (local $ptr i32)
    (local.set $ptr (i32.load (i32.const 0)))
    (i32.store (i32.const 0) (i32.add (local.get $ptr) (local.get $size)))
    (local.get $ptr))

  (func (export "dealloc") (param $ptr i32))

  (func (export "resp_buf") (result i32)
    (i32.const 4096))

  (func (export "resp_len") (result i32)
    (i32.const 8192))

  ;; Writes {"is_correct":true,"feedback":"legacy py_exec"} to resp_buf
  ;; and writes the byte length to *resp_len.
  (func (export "py_exec") (param $req_ptr i32) (param $req_len i32)
    ;; Generated binary stores the response bytes and length here.
  )
)
