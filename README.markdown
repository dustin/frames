Frames is a simple multiplexing protocol I use when I need to ask for
a lot of different things concurrently between two servers and I don't
want to burn lots of file descriptors or rely on pipelining timings
and such things to do it.

This should be usable anywhere you use a `net.Conn` today.  There's
some convenience support for HTTP as I'm using it quite a bit as an
HTTP transport.
