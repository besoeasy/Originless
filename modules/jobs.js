// Nostr job management
const {
  syncNostrPins,
  syncFollowPins,
} = require("./nostr");

const {
  getSelfQueue,
  getFriendsQueue,
  addToSelfQueue,
  addToFriendsQueue,
  removeFromSelfQueue,
  incrementPinnedSelf,
  incrementCachedFriends,
  setLastPinnerActivity,
  setLastNostrRun,
} = require("./queue");

const { isPinned, pinCid, addCid, getCidSize } = require("./nostr");
const { recordPin, updatePinSize, getPinByCid } = require("./database");

let timerProbabilityMethod = 0.9;

// Nostr discovery job
const runNostrJob = async (NPUB) => {
  if (!NPUB) {
    return;
  }

  if (Math.random() < timerProbabilityMethod) {
    timerProbabilityMethod = timerProbabilityMethod - 0.025;
    if (timerProbabilityMethod < 0.2) {
      timerProbabilityMethod = 0.9;
    }
    console.log("Nostr discovery job: Executing (random trigger)");
  } else {
    console.log("Nostr discovery job: Skipping (random delay)");
    return;
  }

  try {
    // Fetch CIDs without pinning (dryRun = true)
    const selfResult = await syncNostrPins({ npubOrPubkey: NPUB, dryRun: true });
    const friendsResult = await syncFollowPins({ npubOrPubkey: NPUB, dryRun: true });

    // Add discovered CIDs to queues (avoid duplicates)
    const selfCids = selfResult.plannedPins || [];
    const friendCids = friendsResult.plannedAdds || [];

    const selfQueue = getSelfQueue();
    const friendsQueue = getFriendsQueue();

    const selfCidSet = new Set(selfQueue.map(obj => obj.cid));
    const friendCidSet = new Set(friendsQueue.map(obj => obj.cid));

    const newSelfCids = selfCids.filter(cidObj => !selfCidSet.has(cidObj.cid));
    const newFriendCids = friendCids.filter(cidObj => !friendCidSet.has(cidObj.cid));

    addToSelfQueue(newSelfCids);
    addToFriendsQueue(newFriendCids);

    setLastNostrRun({
      at: new Date().toISOString(),
      self: {
        eventsScanned: selfResult.eventsScanned,
        cidsFound: selfResult.cidsFound,
        newCids: newSelfCids.length,
        queueSize: selfQueue.length + newSelfCids.length,
      },
      friends: {
        eventsScanned: friendsResult.eventsScanned,
        cidsFound: friendsResult.cidsFound,
        newCids: newFriendCids.length,
        queueSize: friendsQueue.length + newFriendCids.length,
      },
      error: null,
    });

    console.log("\n=== Discovery Summary ===");
    console.log({
      self: {
        discovered: selfCids.length,
        new: newSelfCids.length,
        queueSize: getSelfQueue().length,
      },
      friends: {
        discovered: friendCids.length,
        new: newFriendCids.length,
        queueSize: getFriendsQueue().length,
      },
    });
  } catch (err) {
    setLastNostrRun({
      at: new Date().toISOString(),
      self: null,
      friends: null,
      error: err.message,
    });
    console.error("Nostr discovery job failed", err.message);
  }
};

// Pinner job
const pinnerJob = async () => {
  try {
    const selfQueue = getSelfQueue();
    const friendsQueue = getFriendsQueue();

    console.log(`\n════ Pinner Job Started ════`);
    console.log(`Queue Status: Self=${selfQueue.length}, Friends=${friendsQueue.length}`);

    let didWork = false;

    // Process self queue: pin CID
    if (selfQueue.length > 0) {
      let cidToPinIndex = -1;
      let cidToPin = null;
      const checkedIndices = new Set();

      // Keep trying random CIDs until we find one that's not pinned
      while (checkedIndices.size < selfQueue.length) {
        const randomIndex = Math.floor(Math.random() * selfQueue.length);
        
        if (checkedIndices.has(randomIndex)) {
          continue;
        }
        
        checkedIndices.add(randomIndex);
        const cidObj = selfQueue[randomIndex];
        const cid = cidObj.cid;

        const primalLink = `https://primal.net/e/${cidObj.eventId}`;
        
        console.log(`\n[Self] Checking CID (${selfQueue.length} in queue): ${cid}`);
        console.log(`  Event: ${primalLink}`);
        console.log(`  Author: ${cidObj.author} | Time: ${new Date(cidObj.timestamp * 1000).toISOString()}`);

        const alreadyPinned = await isPinned(cid);
        if (alreadyPinned) {
          console.log(`⏭️  Already pinned, removing from queue: ${cid}`);
          removeFromSelfQueue(randomIndex);
          incrementPinnedSelf();
          didWork = true;
          // Adjust checked indices after splice
          const newCheckedIndices = new Set();
          checkedIndices.forEach(idx => {
            if (idx < randomIndex) {
              newCheckedIndices.add(idx);
            } else if (idx > randomIndex) {
              newCheckedIndices.add(idx - 1);
            }
          });
          checkedIndices.clear();
          newCheckedIndices.forEach(idx => checkedIndices.add(idx));
        } else {
          // Found an unpinned CID
          cidToPinIndex = randomIndex;
          cidToPin = cid;
          break;
        }
      }

      if (cidToPin) {
        const cidObj = selfQueue[cidToPinIndex];
        console.log(`\n[Self] Pinning CID: ${cidToPin}`);
        
        // Record to database first (as pending)
        recordPin({
          eventId: cidObj.eventId,
          cid: cidToPin,
          size: 0,
          timestamp: cidObj.timestamp,
          author: cidObj.author,
          type: 'self',
          status: 'pending'
        });
        
        // Fire-and-forget: start pinning without waiting
        pinCid(cidToPin)
          .then(async () => {
            console.log(`✓ Successfully pinned: ${cidToPin}`);
            // Try to get size after pinning
            try {
              const size = await getCidSize(cidToPin);
              updatePinSize(cidToPin, size, 'pinned');
            } catch (err) {
              updatePinSize(cidToPin, 0, 'pinned');
            }
          })
          .catch(err => {
            console.error(`❌ Failed to pin ${cidToPin}:`, err.message);
            updatePinSize(cidToPin, 0, 'failed');
          });
        
        removeFromSelfQueue(cidToPinIndex);
        incrementPinnedSelf();
        console.log(`📊 Counter updated: totalPinnedSelf = ${incrementPinnedSelf.length}`);
        console.log(`📋 Queue updated: ${getSelfQueue().length} CIDs remaining`);
        didWork = true;
      } else if (checkedIndices.size > 0) {
        console.log(`✓ All checked CIDs were already pinned and removed`);
      }
    } else {
      console.log(`[Self] Queue empty, nothing to process`);
    }

    // Process friends queue: cache CID
    if (friendsQueue.length > 0) {
      const randomIndex = Math.floor(Math.random() * friendsQueue.length);
      const cidObj = friendsQueue[randomIndex];
      const cid = cidObj.cid;

      const primalLink = `https://primal.net/e/${cidObj.eventId}`;
      console.log(`\n[Friend] Caching CID (${friendsQueue.length} in queue): ${cid}`);
      console.log(`  Event: ${primalLink}`);
      console.log(`  Author: ${cidObj.author} | Time: ${new Date(cidObj.timestamp * 1000).toISOString()}`);

      // Record to database first (as pending)
      recordPin({
        eventId: cidObj.eventId,
        cid: cid,
        size: 0,
        timestamp: cidObj.timestamp,
        author: cidObj.author,
        type: 'friend',
        status: 'pending'
      });

      // Fire-and-forget: start caching without waiting
      addCid(cid)
        .then((result) => {
          console.log(`✓ Successfully cached: ${cid}`);
          // Update with actual size from result
          const size = result?.size || 0;
          updatePinSize(cid, size, 'cached');
        })
        .catch(err => {
          console.error(`❌ Failed to cache ${cid}:`, err.message);
          updatePinSize(cid, 0, 'failed');
        });
      
      removeFromFriendsQueue(randomIndex);
      incrementCachedFriends();
      console.log(`📊 Counter updated: totalCachedFriends = ${incrementCachedFriends.length}`);
      console.log(`📋 Queue updated: ${getFriendsQueue().length} CIDs remaining`);
      didWork = true;
    } else {
      console.log(`[Friend] Queue empty, nothing to process`);
    }

    if (didWork) {
      setLastPinnerActivity(new Date().toISOString());
      console.log(`\n⏰ Activity timestamp updated`);
    } else {
      console.log(`\n⏸  No work performed - all queues empty`);
    }

    console.log(`════ Pinner Job Complete ════\n`);
  } catch (err) {
    console.error(`\n❌ Pinner job error:`, err.message);
    console.error(`Stack trace:`, err.stack);
  }
};

module.exports = {
  runNostrJob,
  pinnerJob,
};
