import sys
import os
import time
import random
import uuid
from collections import Counter
import logging
import threading
from flask import Flask, request, jsonify, abort
import kubernetes as kube
import cerberus
import requests

from .globals import max_id, pod_name_job, job_id_job, _log_path, _read_file
from .globals import next_id, make_logger, instance_id

log = make_logger()

REFRESH_INTERVAL_IN_SECONDS = int(os.environ.get('REFRESH_INTERVAL_IN_SECONDS', 5 * 60))


log.info(f'KUBERNETES_TIMEOUT_IN_SECONDS {KUBERNETES_TIMEOUT_IN_SECONDS}')
log.info(f'REFRESH_INTERVAL_IN_SECONDS {REFRESH_INTERVAL_IN_SECONDS}')


v1 = kube.client.CoreV1Api()


log.info(f'instance_id = {instance_id}')


def run_forever(target, *args, **kwargs):
    expected_retry_interval_ms = 15 * 1000

    while True:
        start = time.time()
        run_once(target, *args, **kwargs)
        end = time.time()

        run_time_ms = int((end - start) * 1000 + 0.5)

        sleep_duration_ms = random.randrange(expected_retry_interval_ms * 2) - run_time_ms
        if sleep_duration_ms > 0:
            log.debug(f'run_forever: {target.__name__}: sleep {sleep_duration_ms}ms')
            time.sleep(sleep_duration_ms / 1000.0)


def run_once(target, *args, **kwargs):
    try:
        log.info(f'run_forever: {target.__name__}')
        target(*args, **kwargs)
        log.info(f'run_forever: {target.__name__} returned')
    except Exception:  # pylint: disable=W0703
        log.error(f'run_forever: {target.__name__} caught_exception: ', exc_info=sys.exc_info())


def flask_event_loop():
    app.run(threaded=False, host='0.0.0.0')


def kube_event_loop():
    # May not be thread-safe; opens http connection, so use local version
    v1_ = kube.client.CoreV1Api()

    watch = kube.watch.Watch()
    stream = watch.stream(
        v1_.list_namespaced_pod,
        POD_NAMESPACE,
        label_selector=f'app=batch-job,hail.is/batch-instance={instance_id}')
    for event in stream:
        pod = event['object']
        name = pod.metadata.name
        requests.post('http://127.0.0.1:5000/pod_changed', json={'pod_name': name}, timeout=120)


def polling_event_loop():
    time.sleep(1)
    while True:
        try:
            response = requests.post('http://127.0.0.1:5000/refresh_k8s_state', timeout=120)
            response.raise_for_status()
        except requests.HTTPError as exc:
            log.error(f'Could not poll due to exception: {exc}, text: {exc.response.text}')
        except Exception as exc:  # pylint: disable=W0703
            log.error(f'Could not poll due to exception: {exc}')
        time.sleep(REFRESH_INTERVAL_IN_SECONDS)


def serve():
    kube_thread = threading.Thread(target=run_forever, args=(kube_event_loop,))
    kube_thread.start()

    polling_thread = threading.Thread(target=run_forever, args=(polling_event_loop,))
    polling_thread.start()

    # debug/reloader must run in main thread
    # see: https://stackoverflow.com/questions/31264826/start-a-flask-application-in-separate-thread
    # flask_thread = threading.Thread(target=flask_event_loop)
    # flask_thread.start()
    run_forever(flask_event_loop)

    kube_thread.join()
