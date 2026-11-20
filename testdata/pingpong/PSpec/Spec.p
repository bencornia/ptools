// Counts the pongs the server sends.
spec PongsCounted observes ePong {
    var pongs: int;

    start state Watching {
        on ePong do (count: int) {
            pongs = pongs + 1;
        }
    }
}
