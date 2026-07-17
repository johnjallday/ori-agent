#!/usr/bin/env node

process.stderr.write('Intentional README lifecycle fixture failure.\n');
process.exitCode = 7;
