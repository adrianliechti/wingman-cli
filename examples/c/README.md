# C editor and debugger example

Open this directory as a Wingman workspace and install its managed language
tools. Wingman installs clangd through pip; `compile_flags.txt` supplies the
compiler flags for this project.

In `main.c`, hover over `add`, navigate to its declaration in `arithmetic.h`,
or find references across the source files. Changing an argument to an
incompatible type produces a compiler diagnostic.

Build and run with a C compiler:

```sh
cc -std=c17 -Wall -Wextra main.c arithmetic.c -o wingman-c
./wingman-c
```

Expected output: `20 + 22 = 42`.

To debug, open `main.c` and choose **Debug** above `main`. Wingman offers to
install its managed LLDB debugger if needed, builds `main.c` and `arithmetic.c`
with the flags in `compile_flags.txt` plus `-g -O0`, then stops at `main`.
Set a breakpoint in `add` to inspect the call in `arithmetic.c`.

The compiler must already be installed. Wingman uses `CC` when set, otherwise
`cc`, `clang`, or `gcc`. Each launch rebuilds `.wingman-debug-main` in this
directory so the executable matches the current sources.

The same Run/Debug actions support C++ `main` functions in `.cpp`, `.cc`,
`.cxx`, `.c++`, and `.C` files. C++ builds use `CXX` when set, otherwise `c++`,
`clang++`, or `g++`, and include sibling C++ source files and local
`compile_flags.txt` flags. This automatic build supports small source-directory
projects; it does not invoke CMake, Make, or a mixed C/C++ build.
