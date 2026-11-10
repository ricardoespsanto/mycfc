#!/usr/bin/env python3
"""Run mixed publication plus all twelve unchanged CREATE/EDIT/reuse regressions."""
import pathlib
import runpy
import sys

runner = pathlib.Path(__file__).with_name('run-focused-water-authoring.py')
sys.argv = [str(runner), '--full-create', '--edit-workouts', '--reuse-workouts', '--partial-publication', *sys.argv[1:]]
runpy.run_path(str(runner), run_name='__main__')
