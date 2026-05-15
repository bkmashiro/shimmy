;; echo.wat — minimal guest ABI fixture for wasm package tests.
;;
;; Implements:
;;   alloc(size i32) i32   — bump allocator, heap_top starts at 4
;;   evaluate(req_ptr i32, req_len i32) i32
;;       — ignores input; always returns fixed response {"ok":true}
;;         as a length-prefixed blob: [4-byte LE uint32 len][JSON bytes]
;;
;; The compiled binary (echo.wasm) was generated from this source.
;; {"ok":true} is 11 bytes: 7b 22 6f 6b 22 3a 74 72 75 65 7d
(module
  (memory (export "memory") 1)

  ;; heap_top: mutable i32 global, initialized to 4
  ;; (offset 0..3 reserved so that a NULL/0 pointer is never valid)
  (global $heap_top (mut i32) (i32.const 4))

  ;; alloc(size i32) i32
  (func (export "alloc") (param $size i32) (result i32)
    (local $ptr i32)
    (local.set $ptr (global.get $heap_top))
    (global.set $heap_top
      (i32.add (global.get $heap_top) (local.get $size)))
    (local.get $ptr)
  )

  ;; evaluate(req_ptr i32, req_len i32) i32
  ;; Returns pointer P where:
  ;;   mem[P .. P+4)   = little-endian uint32 length (11)
  ;;   mem[P+4 .. P+15) = {"ok":true}
  (func (export "evaluate") (param $req_ptr i32) (param $req_len i32) (result i32)
    (local $resp_ptr i32)
    ;; Allocate 4 + 11 = 15 bytes for the response blob.
    (local.set $resp_ptr (global.get $heap_top))
    (global.set $heap_top (i32.add (global.get $heap_top) (i32.const 15)))

    ;; Write little-endian length prefix: 11, 0, 0, 0
    (i32.store8 offset=0 (local.get $resp_ptr) (i32.const 11))
    (i32.store8 offset=1 (local.get $resp_ptr) (i32.const 0))
    (i32.store8 offset=2 (local.get $resp_ptr) (i32.const 0))
    (i32.store8 offset=3 (local.get $resp_ptr) (i32.const 0))

    ;; Write {"ok":true}
    (i32.store8 offset=4  (local.get $resp_ptr) (i32.const 0x7b)) ;; {
    (i32.store8 offset=5  (local.get $resp_ptr) (i32.const 0x22)) ;; "
    (i32.store8 offset=6  (local.get $resp_ptr) (i32.const 0x6f)) ;; o
    (i32.store8 offset=7  (local.get $resp_ptr) (i32.const 0x6b)) ;; k
    (i32.store8 offset=8  (local.get $resp_ptr) (i32.const 0x22)) ;; "
    (i32.store8 offset=9  (local.get $resp_ptr) (i32.const 0x3a)) ;; :
    (i32.store8 offset=10 (local.get $resp_ptr) (i32.const 0x74)) ;; t
    (i32.store8 offset=11 (local.get $resp_ptr) (i32.const 0x72)) ;; r
    (i32.store8 offset=12 (local.get $resp_ptr) (i32.const 0x75)) ;; u
    (i32.store8 offset=13 (local.get $resp_ptr) (i32.const 0x65)) ;; e
    (i32.store8 offset=14 (local.get $resp_ptr) (i32.const 0x7d)) ;; }

    (local.get $resp_ptr)
  )
)
