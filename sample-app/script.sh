#!/bin/sh

# This connects to the `ENV GREETING` defined in the Docksmithfile.
echo "-------------------------------------"
echo "  $GREETING from Docksmith!"
echo "-------------------------------------"
echo "Current working directory:"
pwd
echo ""
echo "Files in directory:"
ls -la
echo "Bonus line added."
