#!/usr/bin/env python3
"""Run contextual reuse plus the nine CREATE/EDIT regressions in one new fixture."""
import pathlib
import runpy
import sys

runner = pathlib.Path(__file__).with_name('run-focused-water-authoring.py')
sys.argv = [str(runner), '--full-create', '--edit-workouts', '--reuse-workouts', *sys.argv[1:]]
runpy.run_path(str(runner), run_name='__main__')
