#import <Foundation/Foundation.h>

static id slActivity = nil;

// A window that is hidden is an app macOS is free to nap: its timers stretch
// and its sockets are serviced late. An agent that is meant to stay in its
// hive says it is doing work somebody is waiting for.
void slBeginActivity(void) {
    if (slActivity != nil) return;
    slActivity = [[NSProcessInfo processInfo]
        beginActivityWithOptions:(NSActivityUserInitiatedAllowingIdleSystemSleep)
        reason:@"SuperLeo agent stays in its hive"];
    [slActivity retain];
}

void slEndActivity(void) {
    if (slActivity == nil) return;
    [[NSProcessInfo processInfo] endActivity:slActivity];
    [slActivity release];
    slActivity = nil;
}
