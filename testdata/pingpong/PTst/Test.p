machine Main {
    start state Init {
        entry {
            new Client();
        }
    }
}

test tcPingPong [main=Main]: assert PongsCounted in { Main, Client, Server };
