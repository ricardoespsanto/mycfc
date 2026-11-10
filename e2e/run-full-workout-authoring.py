#!/usr/bin/env python3
"""Run full-CREATE plus plain water regressions through the same owned fixture.
Uses the existing bounded harness; all runtime source must match a frozen manifest.
"""
import pathlib
import runpy
import sys

runner = pathlib.Path(__file__).with_name('run-focused-water-authoring.py')
sys.argv = [str(runner), '--full-create', *sys.argv[1:]]
runpy.run_path(str(runner), run_name='__main__')
