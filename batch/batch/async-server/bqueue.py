from collections import deque
from concurrent.futures import Future
from typing import Any, Optional, Deque
from asyncio import wait_for, wrap_future
# Thanks to David Beazley: https://www.youtube.com/watch?v=x1ndXuw7S0s


class Queue:
    # https://www.python.org/dev/peps/pep-0484/#non-goals
    def __init__(self) -> None:
        self.queue: Deque[Optional(any, Future)] = deque()
        self.getters: Deque[Optional(any, Future)] = deque()

    # get_future returns either of shape (data, result_future)
    # or a future whose .result() will eventually be (data, result_future)
    # This allows us to block
    def get_future(self) -> (any, Optional[Future]):
        # Prefer len over __bool__ : https://github.com/PyCQA/pylint/issues/1405
        # https://stackoverflow.com/questions/43121340/why-is-the-use-of-lensequence-in-condition-values-considered-incorrect-by-pyli#comment73322508_43121340
        if len(self.queue) > 0:
            # if len(self.putters) > 0:
            #     # Unblock the putter queue, so put commands can complete
            #     # Removes the future from the putters deque
            #     self.putters.popleft().set_result(True)
            return self.queue.popleft(), None

        else:
            fut = Future()
            self.getters.append(fut)
            return None, fut

    def get_sync(self):
        promise, future_promise = self.get_future()
        if promise is None:
            promise = future_promise.result()
        return promise

    def put_future(self, item: Any) -> Future:
        fut = Future()
        promise = (item, fut)

        if len(self.getters) > 0:
            # Unblock the getter queue, so get commands can complete
            # And set the future's result to the value it has (kept in the queue)
            # Anyone who has a reference to the future, and calls result()
            # Will get the item
            # Scenario:
            # Person A calls q.get_async()
            # they get a = (None, Future), since no results
            # Person B calls q.put_future("Hello world")
            # Now person A can call a[1].result() and get "Hello world"g,yt, x xbndndn
            self.getters.popleft().set_result(promise)
        else:
            self.queue.append(promise)

        return fut


async def consumer(q: Queue):
    while True:
        item = await q.get_async()
        if item is None:
            break

        print("Async got:", item)


def consumer(q: Queue):
    while True:
        item = q.get_sync()

        print("Sync got:", item)
