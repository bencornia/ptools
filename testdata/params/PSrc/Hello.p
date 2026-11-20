// A parameterised test whose third case fails, and a test that can't.
event eHello: int;

machine Greeter {
    start state Waiting {
        on eHello do (n: int) {
            print format("hello {0}", n);
        }
    }
}

spec NotTwo observes eHello {
    start state Watching {
        on eHello do (n: int) {
            assert n != 2, "got 2";
        }
    }
}

param given: int;

machine Main {
    start state Init {
        entry {
            send new Greeter(), eHello, given;
        }
    }
}

machine Quiet {
    start state Init {
        entry {
            print "quiet";
        }
    }
}

test param (given in [0, 1, 2]) tcHello [main=Main]: assert NotTwo in { Greeter, Main };

test tcQuiet [main=Quiet]: { Quiet };
