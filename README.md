# KRY Compiler (kryc)

Compiler for the KRY UI description language, producing KRB v0.3 binary files.

## Features

*   Parses `.kry` files.
*   Supports `@include` directives.
*   Handles basic component definitions (`Define`) and usage.
*   Resolves styles and properties.
*   Outputs KRB v0.3 binary format.

## Requirements

*   Go toolchain (>= 1.18 recommended)
*   (Optional, Recommended) [Nix](https://nixos.org/) with Flakes enabled

## Building

**Using Go:**

```bash
go build -o kryc .
```