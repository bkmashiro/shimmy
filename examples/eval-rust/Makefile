.PHONY: all clean

all: eval.wasm

eval.wasm:
	rustup target add wasm32-wasip1
	cargo build --target wasm32-wasip1 --release
	cp target/wasm32-wasip1/release/eval_rust.wasm eval.wasm

clean:
	cargo clean
	rm -f eval.wasm
