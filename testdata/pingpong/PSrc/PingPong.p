// A client pings a server, which counts the pings it gets. The server has
// a bug: it counts the third ping twice.
event eHello: Client;
event eHelloBack;
event ePing: (client: Client, n: int);
event ePong: int;

machine Server {
    var count: int;

    start state Serving {
        on eHello do (client: Client) {
            send client, eHelloBack;
        }

        on ePing do (req: (client: Client, n: int)) {
            count = count + 1;
            if (req.n == 3) {
                count = count + 1;
            }
            send req.client, ePong, count;
        }
    }
}

machine Client {
    var server: Server;
    var pinged: int;

    start state Init {
        entry {
            server = new Server();
            Hello();
            goto Pinging;
        }
    }

    state Pinging {
        entry {
            pinged = pinged + 1;
            send server, ePing, (client = this, n = pinged);
        }

        on ePong do (count: int) {
            print format("got pong {0}", count);
            assert count == pinged, format("expected {0} pongs, got {1}", pinged, count);
            if (pinged < 5) {
                goto Pinging;
            }
        }
    }

    fun Hello() {
        send server, eHello, this;
        receive {
            case eHelloBack: {}
        }
    }
}
