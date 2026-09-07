#include <stdio.h>

#include "arithmetic.h"

int main(void) {
    const int result = add(20, 22);
    printf("20 + 22 = %d\n", result);
    return 0;
}
