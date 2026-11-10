// The control server's types, as its JSON has them (proto/view.go,
// proto/ops.go, proto/web.go, proto/netmap.go). Field names follow the json
// tags; Go's time.Time is an RFC 3339 string, absent when zero.

                              
                           
                              
                              
                             
                             
                                 // "tcp/3389" or "tcp/8000-8100,udp/9000"

                                                                                                                     
                                                                             
                                                                                  

                          
                    
                
                                                   
 

                                               
              
 

                         
               
               
                      
                  
                 
 

                                      
                                                           

                         
                   
             
                     
                   
 

                            
                   
                   
                   
                    
                           
 

                                                                                      

                             
               
             
                    
                    
              
                    
                    
                     
 

                                
                   
                      
                    
                   
 

                                      

                          
            
               
                  
                    
                    
                       
                    
                          
                         
                            
                           
                           
                    
                 
 

                            
                     
                     
                    
 

                            
              
                   
                   
                      
                    
 

                             
               
                  
               
                      
               
                   
         
                  
                  
                    
                
                       
                       
                      
                       
                    
                    
 

                               
                 
                     
                 
                   
                      
                   
 

                                                       

                        
              
               
                      
                   
                   
                
                
                   
                    
               
                                                                         
 

                            
              
                
                      
                          
                  
                    
                   
                 
                 
                    
                 
 

                                
             
                  
                    
                    
 

                            
             
               
                  
                  
                    
 

                        
               
                
                    
                     
               
 

                                
                   
             
                  
                  
                     
                    
                      
                          
                      
                                                                        
                                                             
                     
                   
 

                           
                  
               
                   
                  
                        
                  
                         
                               
               
                                
                      
                                
 

                              
                   
                      
                      
                      
 

                            
              
                  
                    
                
 

                            
             
                  
                 
                      
                      
                     
                 
                       
                            
                 
 

                              
                    
                   
 

                            
             
                    
                    
                
             
                    
                      
 

                             
                 
                
                    
                      
                       
                   
                            
                      
 

                             
                
                       
 

                    
                                                                                                                    
                                                                                                               
                                                                                                                            
                                                                           
                                                                                                          
                                                                                                                  
                                                                                                 

                         
              
                    
                    
                
                    
                  
                    
               
                   
                        
                  
                          
                 
                  
                
                  
                    
                
 

                     
             
             
               
 

                           
              
                      
                  
                  
                      
                        
                
 

                           
                  
                    
 

                                                                                                                                          

                            
                                               
 

                                  
                
                
 

                                 
                        
                   
                    
 

// The Web API's paths (proto/web.go).
// The LANs the sidebar lists (DESIGN.md §6.11): a LAN in the device-sharing
// mode is its owner's; its members find what it gives them under 共享给我.
export const sharedOnly = (v          , l         )          => l.mode === 'share' && l.owner !== v.me;
export const listedLANs = (v          )            => v.lans.filter((l) => !l.pending && !sharedOnly(v, l));

export const Paths = {
  login: '/api/v1/login',
  loginEmail: '/api/v1/login/email',
  loginCode: '/api/v1/login/code',
  invites: '/api/v1/invites/',
  logout: '/api/v1/logout',
  userView: '/api/v1/userview',
  stream: '/api/v1/stream',
  ops: '/api/v1/ops',
  relayUsage: '/api/v1/relay-usage',
  invite: '/api/v1/invite',
}         ;

// The invite link's path on the server (proto/links.go InvitePath).
export const InvitePath = '/i/';

// fnv32a is FNV-1a over the string's UTF-8 bytes, as Go's hash/fnv New32a.
export function fnv32a(s        )         {
  let h = 0x811c9dc5;
  for (const b of new TextEncoder().encode(s)) {
    h ^= b;
    h = Math.imul(h, 0x01000193) >>> 0;
  }
  return h >>> 0;
}

// colorIndex is a person's owner color on my screen, as the desktop picks
// it (client/model ColorFor): blue (0) for me, and for anyone else one of
// the other five by their id, the same in every LAN and on every visit.
export function colorIndex(me          , id          )         {
  if (id === me) return 0;
  return 1 + (fnv32a(id) % 5);
}
