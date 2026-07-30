# Included after CPython's generated WASI Makefile.

.PHONY: shimmy-python-runtime

shimmy-python-runtime:
	@test -n "$(SHIMMY_RUNTIME_SOURCE)"
	@test -n "$(SHIMMY_RUNTIME_INCLUDE)"
	@test -n "$(SHIMMY_GENERATED_INCLUDE)"
	@test -n "$(SHIMMY_WASI_VFS_LIBRARY)"
	@test -n "$(SHIMMY_OUTPUT)"
	$(CC) $(PY_CORE_CFLAGS) \
		-I$(SHIMMY_RUNTIME_INCLUDE) \
		-I$(SHIMMY_GENERATED_INCLUDE) \
		-c $(SHIMMY_RUNTIME_SOURCE) \
		-o shimmy_python_runtime.o
	$(LINKCC) $(PY_CORE_LDFLAGS) $(LINKFORSHARED) \
		-mexec-model=reactor \
		-Wl,-z,stack-size=16777216 \
		-Wl,--stack-first \
		-Wl,--initial-memory=268435456 \
		-Wl,--max-memory=2147483648 \
		-Wl,--export-memory \
		-Wl,--export=shimmy_python_runtime_identity \
		-Wl,--export=shimmy_python_init \
		-Wl,--export=shimmy_python_prepare \
		-Wl,--export=alloc \
		-Wl,--export=dealloc \
		-Wl,--export=evaluate \
		-o $(SHIMMY_OUTPUT) \
		shimmy_python_runtime.o \
		$(BLDLIBRARY) $(LIBS) $(MODLIBS) $(SYSLIBS) \
		$(SHIMMY_WASI_VFS_LIBRARY)
