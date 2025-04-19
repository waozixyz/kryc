#!/usr/bin/env bash

# Directory containing the example files
EXAMPLES_DIR="../kryon/examples"

# Check if the examples directory exists
if [ ! -d "$EXAMPLES_DIR" ]; then
  echo "Error: Directory $EXAMPLES_DIR does not exist"
  exit 1
fi

# Find all .kry files and compile them
for file in "$EXAMPLES_DIR"/*.kry; do
  # Check if there are any .kry files
  if [ -f "$file" ]; then
    # Generate output filename by replacing .kry with .krb
    output="${file%.kry}.krb"
    echo "Compiling $file to $output"
    ./kryc "$file" "$output"
    
    # Check if compilation was successful
    if [ $? -eq 0 ]; then
      echo "Successfully compiled $file"
    else
      echo "Error compiling $file"
    fi
  else
    echo "No .kry files found in $EXAMPLES_DIR"
    exit 0
  fi
done

