## Golang HTTP Longpoll Server

This library is **heavily** inspired by J Cuga's
[golongpoll](https://github.com/jcuga/golongpoll) project. I just needed
to simplify some things.

### Main Changes

* No more categories - each category needs to have its own longpoll
  instance.
* Last Value Cache - when creating a new longpoll manager, you can
  specify if it should just cache the last published value rather than
  maintain a history of published values.
