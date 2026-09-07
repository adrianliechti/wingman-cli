# C language-server example

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
